// Package cathexis implements the Cathexis MVR5/MVR7 mobile-DVR unit type as a
// gateway plugin: device registration/approval, GPS + event telemetry to the
// universal webhook, live video (VideoController + MediaServerProvider), recorded
// clips (uploaded as finished MP4s), device parameter config (ConfigController),
// and a minimal live status snapshot (StatusReporter).
//
// Wire format and command set are ported from the old Node gateway
// (dfm-mvr-gateway src/tcp/controlServer.js, videoServer.js, clipReceiver.js).
package cathexis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

const (
	deviceMake = "cathexis"
	// Port layout inherited from the old Node gateway, which a provisioned device
	// expects: 32324 control (the device dials it), 32325 + 32326 media. The old
	// gateway split media into a clip receiver (32325) and a live-stream server
	// (32326); this unit serves both from one handler, so it listens on the media
	// port AND the next port to accept a device that targets either.
	defaultControlPort  = 32324
	defaultMediaPortNum = 32325
	idleTimeout         = 3 * time.Minute
)

// Protocol is the Cathexis unit-type plugin.
type Protocol struct{}

// New returns a Cathexis protocol plugin.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return "cathexis" }

func (*Protocol) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{HasVideo: true, HasCommands: true, HasConfig: true, HasStatus: true, HasReview: true}
}

// DefaultDevicePort is the control port Cathexis devices dial.
func (*Protocol) DefaultDevicePort() int { return defaultControlPort }

// DefaultMediaPort keeps the Cathexis media listener off Howen's media port when
// both video units run in one process.
func (*Protocol) DefaultMediaPort() int { return defaultMediaPortNum }

// IdleTimeout widens the read deadline — Cathexis control sockets are quiet
// between GPS reports (the device heartbeats every few minutes).
func (*Protocol) IdleTimeout() time.Duration { return idleTimeout }

// MappingProvider: Cathexis event output is driven by an editable
// device-event → event-code table (string device names mapped onto stable
// synthetic integer codes — see events.go). These thin methods let the app runner
// seed defaults and apply the DB set without importing this package.
func (*Protocol) DefaultMappingEntries() []mapping.Entry { return DefaultMappingEntries() }
func (*Protocol) ApplyMappings(byModel mapping.ByModel)  { ApplyMappings(byModel) }

// ReadFrame decodes one Cathexis frame (12-byte header + payload).
func (*Protocol) ReadFrame(r *bufio.Reader) (gateway.Frame, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return gateway.Frame{}, err
	}
	h, err := readHeader(hdr[:])
	if err != nil {
		return gateway.Frame{}, err
	}
	if h.Size > maxFramePayld {
		return gateway.Frame{}, fmt.Errorf("cathexis payload too large: %d", h.Size)
	}
	payload := make([]byte, h.Size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return gateway.Frame{}, err
	}
	return gateway.Frame{Type: h.Type, Payload: payload}, nil
}

func (*Protocol) NewSession(c *gateway.Conn) gateway.Session {
	return &session{conn: c, pending: map[string]chan map[string]any{}, stop: make(chan struct{})}
}

type session struct {
	conn     *gateway.Conn
	serial   string
	model    string
	approved bool
	// connType is the welcome's connection_type ("control", "event", "video", …).
	// Only the control connection is registered in the Hub as the command channel
	// and owns the device's online/sleep state; a device opens several connections
	// to the control port and registering a non-control one would clobber commands.
	connType string
	// lifecycle is the control connection's view of device state: "online" or
	// "sleep" (standby). Driven by power-state events (entered_standby/deep_sleep/
	// wake_*) and GPS activity; gates video/config requests that standby won't serve.
	lifecycle string

	// pending correlates an in-flight command to the device response type it awaits
	// (Cathexis matches responses by message type, not a session id). Guarded by
	// pendingMu because the request runs on the HTTP goroutine while the read loop
	// delivers responses.
	pendingMu sync.Mutex
	pending   map[string]chan map[string]any

	// latest caches the most recent telemetry for the device-detail status view;
	// sdHealth/environment cache the last polled SD-card and environment reports.
	statusMu      sync.Mutex
	latest        map[string]any
	latestAt      time.Time
	sdHealth      map[string]any
	sdHealthAt    time.Time
	environment   map[string]any
	environmentAt time.Time

	// stop ends the background status poller when the connection closes.
	stop     chan struct{}
	stopOnce sync.Once
}

// OnFrame dispatches a decoded frame.
func (s *session) OnFrame(ctx context.Context, f gateway.Frame) error {
	switch f.Type {
	case frameHeartbeat, frameAck:
		return nil
	case frameJSON:
		env, ok := parseEnvelope(f.Payload)
		if !ok {
			return nil
		}
		return s.handleJSON(ctx, env)
	case frameEventPreview:
		s.handleEventPreview(ctx, f.Payload)
		return nil
	default:
		s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{"event": "unhandled_frame", "type": f.Type, "serial": s.serialOrUnknown()})
		return nil
	}
}

func (s *session) handleJSON(ctx context.Context, env envelope) error {
	if env.Type == "welcome" {
		return s.handleWelcome(ctx, env.Payload)
	}
	if !s.approved {
		return nil
	}
	// A device error (payload {category, message}) fails any in-flight request so
	// it doesn't hang to the timeout; an unsolicited one is just persisted.
	if env.Type == "error" {
		s.handleDeviceError(env.Payload)
		return nil
	}
	// A pending command awaiting this response type takes it first.
	if ch := s.takePending(env.Type); ch != nil {
		ch <- env.Payload
		return nil
	}
	switch env.Type {
	case "gps":
		// GPS flows at 1Hz only while the unit is awake, so any GPS confirms online.
		s.setLifecycle(ctx, "online")
		p := s.buildTelemetry(env.Payload, false)
		s.recordStatus(p)
		s.conn.Emit(s.serial, deviceMake, s.model, "gps", p)
	case "event":
		p := s.buildTelemetry(env.Payload, true)
		s.reconcileLifecycle(ctx, toString(env.Payload["name"]))
		s.recordStatus(p)
		s.conn.Emit(s.serial, deviceMake, s.model, "event", p)
	default:
		s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{"event": "unsolicited_message", "type": env.Type, "serial": s.serial})
	}
	return nil
}

// handleDeviceError routes a device "error" message: if a command is in flight it
// fails it fast (so callers see a real error instead of a timeout); otherwise the
// error is persisted to device_errors for the admin Logs view.
func (s *session) handleDeviceError(payload map[string]any) {
	msg := strings.TrimSpace(toString(payload["message"]))
	if msg == "" {
		msg = strings.TrimSpace(toString(payload["text"]))
	}
	category := toString(payload["category"])
	if s.failPending(msg) {
		return
	}
	raw, _ := json.Marshal(payload)
	s.conn.EmitDeviceError(s.serial, category, msg, raw)
	s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{"event": "device_error", "serial": s.serial, "category": category, "message": msg})
}

func (s *session) handleWelcome(ctx context.Context, payload map[string]any) error {
	log := s.conn.Deps.Log.With("tcp/cathexis")
	rawSerial := strings.TrimSpace(toString(payload["serial"]))
	if rawSerial == "" {
		log.Debug(map[string]any{"event": "welcome_missing_serial", "remote": s.conn.RemoteAddr().String()})
		return nil
	}
	s.serial = device.NormalizeSerial(rawSerial)
	s.model = s.serial
	if i := strings.IndexAny(s.serial, "_ "); i > 0 {
		s.model = s.serial[:i]
	}
	// A device opens several connections to the control port (control, event,
	// video, …); the welcome's connection_type tells them apart. Default to
	// "control" when absent (older firmware / the common case).
	s.connType = strings.ToLower(strings.TrimSpace(toString(payload["connection_type"])))
	if s.connType == "" {
		s.connType = "control"
	}

	// Acknowledge (type 16) when the device asks for it, so it proceeds to send
	// subsequent packets (previews/events).
	if b, _ := payload["requires_ack"].(bool); b {
		_ = s.conn.WriteFrame(buildAck())
	}

	result, err := s.conn.Deps.Auth.Authorize(ctx, device.RegisterInfo{
		Serial:   s.serial,
		Protocol: deviceMake,
		RemoteIP: s.conn.RemoteIP(),
		Meta: map[string]any{
			"message_type":    "welcome",
			"connection_type": payload["connection_type"],
			"firmware":        payload["firmware"],
			"api":             payload["api"],
		},
	})
	if err != nil {
		log.Error(map[string]any{"event": "device_gate_error", "serial": s.serial, "error": err.Error()})
		return nil
	}
	if !result.Known {
		s.approved = false
		log.Info(map[string]any{"event": "unknown_device_quarantined", "serial": s.serial})
		return nil
	}

	s.approved = true

	// Only the control connection becomes the command channel + state owner.
	// Other connection types (event-preview, video, …) are approved so their
	// frames are processed, but must not register in the Hub (it would overwrite
	// the control connection's commander entry and break commands).
	if s.connType != "control" {
		log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "model": s.model, "connection_type": s.connType})
		return nil
	}

	s.lifecycle = "online"
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "online"); err != nil {
		log.Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "online", "error": err.Error()})
	}
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Register(gateway.DeviceInfo{
			Serial:      s.serial,
			Protocol:    deviceMake,
			Model:       s.model,
			RemoteAddr:  s.conn.RemoteAddr().String(),
			ConnectedAt: time.Now().UTC(),
			State:       "online",
		}, s)
	}
	log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "model": s.model, "connection_type": s.connType})
	go s.statusPoller()
	return nil
}

func (s *session) OnClose(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stop) })
	if s.serial == "" || !s.approved {
		return
	}
	current := true
	if s.conn.Deps.Hub != nil {
		current = s.conn.Deps.Hub.Unregister(s.serial, s)
	}
	if current {
		if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "offline"); err != nil {
			s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "offline", "error": err.Error()})
		}
	}
}

// ---- command/response correlation ----

func (s *session) registerPending(respType string) (chan map[string]any, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, exists := s.pending[respType]; exists {
		return nil, fmt.Errorf("another %s request is in progress", respType)
	}
	ch := make(chan map[string]any, 1)
	s.pending[respType] = ch
	return ch, nil
}

func (s *session) takePending(respType string) chan map[string]any {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	ch := s.pending[respType]
	if ch != nil {
		delete(s.pending, respType)
	}
	return ch
}

func (s *session) clearPending(respType string) {
	s.pendingMu.Lock()
	delete(s.pending, respType)
	s.pendingMu.Unlock()
}

// failPending delivers a device error to every in-flight request (Cathexis sends
// a generic "error" envelope, not one keyed to the request, so a single error
// fails whatever is waiting). Returns whether anything was waiting.
func (s *session) failPending(msg string) bool {
	if msg == "" {
		msg = "device reported an error"
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if len(s.pending) == 0 {
		return false
	}
	for k, ch := range s.pending {
		ch <- map[string]any{"error": msg}
		delete(s.pending, k)
	}
	return true
}

// ---- lifecycle (online/sleep) ----

// reconcileLifecycle maps a power-state event name to the device's online/sleep
// state. Non-lifecycle events are ignored (they don't change state).
func (s *session) reconcileLifecycle(ctx context.Context, name string) {
	switch sanitizeEventKey(name) {
	case "entered_standby", "deep_sleep", "wake_dapi_off", "wake_imu_off":
		// wake_*_off means the unit re-entered standby after a brief wake.
		s.setLifecycle(ctx, "sleep")
	case "wake_dapi_on", "wake_imu_on", "ignition_on", "motion_start":
		s.setLifecycle(ctx, "online")
	}
}

// setLifecycle flips the device's online/sleep state, deduping on the last value.
// Only the control connection owns state (it is the registered commander).
func (s *session) setLifecycle(ctx context.Context, desired string) {
	if !s.approved || s.connType != "control" || s.lifecycle == desired {
		return
	}
	s.lifecycle = desired
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.SetState(s.serial, s, desired)
	}
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, desired); err != nil {
		s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{
			"event": "device_status_update_failed", "serial": s.serial, "status": desired, "error": err.Error(),
		})
	}
}

// ---- event-preview snapshots (type 15) ----

// handleEventPreview decodes a pushed event-preview frame (road/cab JPEGs) and
// saves each image to the gateway bucket. Saving runs off the read loop so a slow
// disk/DB never stalls frame processing.
func (s *session) handleEventPreview(ctx context.Context, payload []byte) {
	if !s.approved {
		return
	}
	log := s.conn.Deps.Log.With("tcp/cathexis")
	ep, ok := parseEventPreview(payload)
	if !ok {
		log.Debug(map[string]any{"event": "event_preview_bad_frame", "serial": s.serial, "len": len(payload)})
		return
	}
	saver := s.conn.Deps.SnapshotSaver
	if saver == nil {
		log.Debug(map[string]any{"event": "event_preview_dropped_no_storage", "serial": s.serial, "name": ep.Name})
		return
	}
	serial := s.serial
	kind := sanitizeEventKey(ep.Name)
	if kind == "" {
		kind = "event"
	}
	// Copy the JPEG bytes: the payload buffer is reused by the read loop.
	type img struct {
		camera int
		data   []byte
	}
	var imgs []img
	if len(ep.Road) > 0 {
		imgs = append(imgs, img{0, append([]byte(nil), ep.Road...)})
	}
	if len(ep.Cab) > 0 {
		imgs = append(imgs, img{1, append([]byte(nil), ep.Cab...)})
	}
	if len(imgs) == 0 {
		return
	}
	utc := ep.UTC
	go func() {
		defer func() { _ = recover() }()
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, im := range imgs {
			id, err := saver.Save(sctx, serial, im.camera, kind, "event", utc, im.data)
			if err != nil {
				log.Error(map[string]any{"event": "event_preview_save_failed", "serial": serial, "name": kind, "camera": im.camera, "error": err.Error()})
				continue
			}
			log.Info(map[string]any{"event": "event_preview_saved", "serial": serial, "name": kind, "camera": im.camera, "id": id, "bytes": len(im.data)})
		}
	}()
}

// request sends a command and waits for the device's reply of respType.
func (s *session) request(ctx context.Context, cmdType string, payload map[string]any, respType string) (map[string]any, error) {
	ch, err := s.registerPending(respType)
	if err != nil {
		return nil, err
	}
	defer s.clearPending(respType)
	if err := s.conn.WriteFrame(buildCommand(cmdType, payload)); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case resp := <-ch:
		if msg := strings.TrimSpace(toString(resp["error"])); msg != "" {
			return nil, fmt.Errorf("device error: %s", msg)
		}
		return resp, nil
	}
}

// ---- Commander ----

func (s *session) SupportedCommands() []string { return []string{"reboot_unit", "wake_device"} }

func (s *session) SendCommand(ctx context.Context, cmd gateway.Command) (gateway.CommandResult, error) {
	if s.serial == "" || !s.approved {
		return gateway.CommandResult{}, gateway.ErrNotConnected
	}
	switch cmd.Type {
	case "reboot_unit":
		if err := s.conn.WriteFrame(buildCommand("reboot_unit", map[string]any{})); err != nil {
			return gateway.CommandResult{}, err
		}
		// The device reboots and won't reliably answer; report acceptance.
		return gateway.CommandResult{Data: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}, nil
	case "wake_device":
		// Cathexis has no dedicated wake command — the unit wakes from standby on
		// ANY dAPI message over its (still-open) control socket (API §4.1
		// wake_dapi_on, "eg request for live"). Send a lightweight request as the
		// poke, fire-and-forget: the device is waking and we don't wait for a reply.
		// It then emits wake_dapi_on, which flips lifecycle back to online.
		if err := s.conn.WriteFrame(buildCommand("request_sd_health", map[string]any{})); err != nil {
			return gateway.CommandResult{}, err
		}
		return gateway.CommandResult{Data: map[string]any{"ok": true, "note": "wake poke sent"}, ReceivedAt: time.Now().UTC()}, nil
	default:
		return gateway.CommandResult{}, gateway.ErrUnsupportedCommand
	}
}

// ---- StatusReporter ----

func (s *session) recordStatus(p map[string]any) {
	s.statusMu.Lock()
	s.latest = p
	s.latestAt = time.Now().UTC()
	s.statusMu.Unlock()
}

// ---- telemetry payload ----

// buildTelemetry normalizes a device GPS/event payload into the field names the
// universal message builder understands: speed is converted m/s → km/h, numeric
// fields are coerced, and events carry the mapped standard codes.
func (s *session) buildTelemetry(raw map[string]any, isEvent bool) map[string]any {
	p := map[string]any{}
	for _, k := range []string{"latitude", "longitude", "altitude", "accuracy", "bearing", "satellites", "utc"} {
		if v, ok := raw[k]; ok {
			if f, ok := toFloat(v); ok {
				p[k] = f
			}
		}
	}
	if v, ok := raw["speed"]; ok {
		if f, ok := toFloat(v); ok {
			p["speed"] = math.Round(f*3.6*100) / 100 // m/s → km/h
		}
	}
	if v, ok := raw["ignition"]; ok {
		p["ignition"] = v
	}
	if isEvent {
		p["event"] = toStandardEventCodes(raw, true)
	}
	return p
}

func (s *session) serialOrUnknown() string {
	if s.serial == "" {
		return "unknown"
	}
	return s.serial
}

// mediaTarget splits the configured media advertise host (host:port) into the ip
// and port fields the device expects in stream/clip commands.
func (s *session) mediaTarget() (string, int, error) {
	host, portStr, err := net.SplitHostPort(s.conn.Deps.MediaAdvertiseHost)
	if err != nil {
		return "", 0, errors.New("video is not enabled on this gateway")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, errors.New("invalid media advertise port")
	}
	return host, port, nil
}
