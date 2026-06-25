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
	deviceMake          = "cathexis"
	defaultControlPort  = 33010
	defaultMediaPortNum = 33011
	idleTimeout         = 3 * time.Minute
)

// Protocol is the Cathexis unit-type plugin.
type Protocol struct{}

// New returns a Cathexis protocol plugin.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return "cathexis" }

func (*Protocol) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{HasVideo: true, HasCommands: true, HasConfig: true, HasStatus: true}
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
	return &session{conn: c, pending: map[string]chan map[string]any{}}
}

type session struct {
	conn     *gateway.Conn
	serial   string
	model    string
	approved bool

	// pending correlates an in-flight command to the device response type it awaits
	// (Cathexis matches responses by message type, not a session id). Guarded by
	// pendingMu because the request runs on the HTTP goroutine while the read loop
	// delivers responses.
	pendingMu sync.Mutex
	pending   map[string]chan map[string]any

	// latest caches the most recent telemetry for the device-detail status view.
	statusMu sync.Mutex
	latest   map[string]any
	latestAt time.Time
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
	// A pending command awaiting this response type takes it first.
	if ch := s.takePending(env.Type); ch != nil {
		ch <- env.Payload
		return nil
	}
	switch env.Type {
	case "gps":
		p := s.buildTelemetry(env.Payload, false)
		s.recordStatus(p)
		s.conn.Emit(s.serial, deviceMake, s.model, "gps", p)
	case "event":
		p := s.buildTelemetry(env.Payload, true)
		s.recordStatus(p)
		s.conn.Emit(s.serial, deviceMake, s.model, "event", p)
	default:
		s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{"event": "unsolicited_message", "type": env.Type, "serial": s.serial})
	}
	return nil
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

	// Control-channel ack (type 16) when the device asks for it.
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
	log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "model": s.model})
	return nil
}

func (s *session) OnClose(ctx context.Context) {
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

func (s *session) SupportedCommands() []string { return []string{"reboot_unit"} }

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

func (s *session) Status() (map[string]any, bool) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.latest == nil {
		return nil, false
	}
	loc := map[string]any{}
	for _, k := range []string{"latitude", "longitude", "speed", "altitude", "satellites", "bearing"} {
		if v, ok := s.latest[k]; ok {
			loc[k] = v
		}
	}
	snap := map[string]any{
		"updated_at": s.latestAt.Format(time.RFC3339),
		"location":   loc,
	}
	if v, ok := s.latest["ignition"]; ok {
		snap["vehicle"] = map[string]any{"ignition": v}
	}
	return map[string]any{"telemetry": snap}, true
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
