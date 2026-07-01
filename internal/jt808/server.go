// Package jt808 implements the JT/T 808-2019 unit type for the N62 fleet dashcam
// as a gateway plugin: registration/auth, GPS + event telemetry to the universal
// webhook, device parameter config (ConfigController over ULV 0xB050/0xB051), a
// live status snapshot (StatusReporter from ULV transparent 0x0900), control
// commands (Commander), an editable per-unit timezone setting (ConfigurableUnit),
// and JT1078 video over a separate media port — live HLS (VideoController +
// MediaServerProvider), recorded clips, and a recording file query (see media.go,
// video.go, recordings.go). On-demand snapshots (snapshot.go) are wired but
// declared off, as the validated N62 firmware ignores the camera-shoot command.
//
// Wire format and command set are ported from the old Node gateway
// (dfm-mvr-gateway/src/tcp/jt808Server.js), validated against this exact device.
package jt808

import (
	"bufio"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

const (
	// protocolMake is the universal-message make for every JT808 device, kept
	// "jt808_19" so the message-builder's jt808Switch emits the historical device
	// type regardless of which make/model unit hosts it.
	protocolMake = "jt808_19"
	idleTimeout  = 6 * time.Minute
)

// Config describes one concrete JT808 unit type (a make/model, or a group of
// unbranded models) hosted on the shared JT808 codec. Different vendors/models are
// registered as SEPARATE units — each on its own control/media port — so their
// settings, capabilities, ports and event-mapping tables are independent. Register
// more with another jt808.New(Config{...}) line in cmd/gateway.
type Config struct {
	Unit         string // registry protocol + log namespace + DB key, e.g. "dfm-n62"
	Model        string // universal-message model, e.g. "N62"
	Make         string // universal-message make; defaults to "jt808_19"
	ControlPort  int    // JT808 control listener
	MediaPort    int    // JT1078 media listener
	HasSnapshots bool   // N62 firmware ignores 0x8801; a model that honours it sets true
}

// N62 is the preset for the unbranded "DFM" N62-class dashcam group (any no-vendor
// N62-class device), on the historical JT808 ports.
func N62() Config {
	return Config{Unit: "dfm-n62", Model: "N62", Make: protocolMake, ControlPort: 6608, MediaPort: 6609}
}

// Protocol is a JT808 unit-type plugin instance. routes is shared between control
// sessions (which start streams/clips) and the media listener (which routes the
// device's JT1078 connections; see media.go). mappings holds THIS unit's active
// per-model event-mapping tables — per-instance so multiple JT808 units hosted in
// one process don't share/clobber mapping state.
type Protocol struct {
	cfg      Config
	routes   *streamRoutes
	mappings atomic.Pointer[map[string]*Mappings]
}

// New returns a JT808 unit-type plugin for the given make/model config.
func New(cfg Config) *Protocol {
	if cfg.Make == "" {
		cfg.Make = protocolMake
	}
	p := &Protocol{cfg: cfg, routes: newStreamRoutes()}
	m := map[string]*Mappings{"": defaultMappings()}
	p.mappings.Store(&m)
	return p
}

func (p *Protocol) Name() string  { return p.cfg.Unit }
func (p *Protocol) logNS() string { return "tcp/" + p.cfg.Unit }

func (p *Protocol) Capabilities() gateway.Capabilities {
	// HasSnapshots is per-model: the validated N62 firmware ignores the on-demand
	// camera-shoot command (0x8801) — it sends no response at all — so the capture
	// UI would always fail. The Snapshotter methods and the auto-push 0x0801 handler
	// remain (stills the device pushes on events/interval are still reassembled and
	// saved); a model whose firmware supports 0x8801 sets Config.HasSnapshots.
	return gateway.Capabilities{HasVideo: true, HasCommands: true, HasConfig: true, HasStatus: true, HasSnapshots: p.cfg.HasSnapshots}
}

// DefaultDevicePort is the JT808 control port the device dials (also where it sends
// JT1078 video unless we advertise the media port — which we do).
func (p *Protocol) DefaultDevicePort() int { return p.cfg.ControlPort }

// DefaultMediaPort keeps this unit's JT808 media listener off the other units'
// ports when hosted in one process.
func (p *Protocol) DefaultMediaPort() int { return p.cfg.MediaPort }

// IdleTimeout widens the read deadline: the device heartbeats/reports periodically
// but can be quiet between fixes.
func (*Protocol) IdleTimeout() time.Duration { return idleTimeout }

// MappingProvider: JT808 event output is driven by editable alarm-bit / ULV /
// vendor-TLV → event-code tables (see events.go), per unit instance.
func (*Protocol) DefaultMappingEntries() []mapping.Entry  { return DefaultMappingEntries() }
func (p *Protocol) ApplyMappings(byModel mapping.ByModel) { p.applyMappings(byModel) }

// settingTimezoneOffset is the editable per-unit setting key for the device's
// local-clock offset from UTC.
const settingTimezoneOffset = "timezone_offset"

// SettingsSchema declares the unit's editable gateway-side settings (rendered as
// the JT808 settings screen). Implements gateway.ConfigurableUnit. The timezone
// offset converts the N62's local wall-clock (in 0x0200 locations and the video
// time windows) to/from UTC, editable from the admin without a redeploy.
func (*Protocol) SettingsSchema() []gateway.SettingField {
	return []gateway.SettingField{{
		Key:     settingTimezoneOffset,
		Label:   "Device timezone offset (hours)",
		Type:    "number",
		Default: "0",
		Help:    "The N62 reports local wall-clock with no timezone. Set e.g. 2 for SAST, or 0 if the device already sends UTC (GpsSync). Applies to location timestamps and recorded-video time windows.",
		Group:   "Time",
	}}
}

// ReadFrame decodes one JT808 frame: it scans to a 0x7e start flag, reads to the
// next flag, unescapes, validates the XOR checksum, and returns the message ID as
// the frame type with the unescaped header+body (checksum stripped) as payload.
// The session re-parses the header from the payload.
func (*Protocol) ReadFrame(r *bufio.Reader) (gateway.Frame, error) {
	// Discard bytes until a start flag.
	for {
		b, err := r.ReadByte()
		if err != nil {
			return gateway.Frame{}, err
		}
		if b == flag {
			break
		}
	}
	// Read inter-flag content, skipping empty runs (back-to-back frames may share
	// a flag, yielding a zero-length segment).
	for {
		raw, err := r.ReadBytes(flag)
		if err != nil {
			return gateway.Frame{}, err
		}
		content := raw[:len(raw)-1] // drop the trailing flag
		if len(content) == 0 {
			continue
		}
		if len(content) > maxFrameBytes {
			return gateway.Frame{}, fmt.Errorf("jt808: frame too large (%d bytes)", len(content))
		}
		unesc, err := unescape(content)
		if err != nil {
			return gateway.Frame{}, err
		}
		if len(unesc) < 3 { // need >=2 message-id bytes + a checksum byte
			return gateway.Frame{}, fmt.Errorf("jt808: runt frame (%d bytes)", len(unesc))
		}
		payload := unesc[:len(unesc)-1]
		want := unesc[len(unesc)-1]
		if got := xorChecksum(payload); got != want {
			return gateway.Frame{}, fmt.Errorf("jt808: bad checksum (got 0x%02x want 0x%02x)", got, want)
		}
		msgID := int(payload[0])<<8 | int(payload[1])
		return gateway.Frame{Type: msgID, Payload: payload}, nil
	}
}

func (p *Protocol) NewSession(c *gateway.Conn) gateway.Session {
	return &session{proto: p, conn: c, routes: p.routes, model: p.cfg.Model, pending: map[int]chan map[string]any{}, frameReasm: map[uint16]*frameReasm{}}
}

// frameReasm accumulates the body fragments of a subpackaged JT808 message (one 0x7E
// frame per package, sharing a MsgID with per-frame SubTotal/SubIndex) until every
// package has arrived.
type frameReasm struct {
	total uint16
	parts map[uint16][]byte
}

type session struct {
	proto  *Protocol // owning unit-type (make/model, ports, mapping tables)
	conn   *gateway.Conn
	routes *streamRoutes
	serial string // gateway serial "JT808_<digits>"
	phone  string // raw terminal-phone digits (for addressing replies)
	model  string // device model ("N62"), from the unit config

	approved  bool
	lifecycle string // "online" | "offline"

	serialMu   sync.Mutex
	platSerial uint16 // platform message serial counter

	// pending correlates an in-flight platform request to the device response it
	// awaits, keyed by the response message ID (and a synthetic key for transparent
	// 0x0900 replies — see transparentPendingKey). Guarded by pendingMu because
	// requests run on the HTTP goroutine while the read loop delivers responses.
	pendingMu sync.Mutex
	pending   map[int]chan map[string]any

	// status caches the last basic-status / SD-health parsed from 0x0900.
	statusMu    sync.Mutex
	basicStatus map[string]any
	basicAt     time.Time
	sdHealth    map[string]any
	sdHealthAt  time.Time

	// snapshot capture (0x8801 -> 0x0801 image upload, possibly subpackaged).
	snapMu     sync.Mutex
	snapReasm  *multimediaReasm
	snapWaiter chan []byte

	// frameReasm reassembles subpackaged non-multimedia messages (e.g. a large ULV
	// 0xB051 config reply the N62 splits across frames), keyed by MsgID.
	frameReasmMu sync.Mutex
	frameReasm   map[uint16]*frameReasm
}

// reassemble buffers one subpackaged body fragment and returns the concatenated body
// once all packages (1..total) have arrived. Fragments of different messages are kept
// apart by MsgID; a change in the advertised total resets the buffer.
func (s *session) reassemble(h header, body []byte) ([]byte, bool) {
	s.frameReasmMu.Lock()
	defer s.frameReasmMu.Unlock()
	if s.frameReasm == nil {
		s.frameReasm = map[uint16]*frameReasm{}
	}
	b := s.frameReasm[h.MsgID]
	if b == nil || b.total != h.SubTotal {
		b = &frameReasm{total: h.SubTotal, parts: map[uint16][]byte{}}
		s.frameReasm[h.MsgID] = b
	}
	b.parts[h.SubIndex] = append([]byte(nil), body...)
	if len(b.parts) < int(b.total) {
		return nil, false
	}
	full := make([]byte, 0, len(body)*int(b.total))
	for i := uint16(1); i <= b.total; i++ {
		full = append(full, b.parts[i]...)
	}
	delete(s.frameReasm, h.MsgID)
	return full, true
}

func (s *session) log() *logging.Logger { return s.conn.Deps.Log.With(s.proto.logNS()) }

// tzOffset is the device's local-clock offset from UTC: the editable per-unit
// setting (SettingsSchema), falling back to the env-configured global (and for
// tests, which build Deps without a settings holder).
func (s *session) tzOffset() float64 {
	tz := s.conn.Deps.Config.DeviceTZOffsetHours
	if us := s.conn.Deps.UnitSettings; us != nil {
		tz = us.Float(settingTimezoneOffset, tz)
	}
	return tz
}

// nextPlatSerial returns the next platform message serial (starts at 0, wraps).
func (s *session) nextPlatSerial() uint16 {
	s.serialMu.Lock()
	defer s.serialMu.Unlock()
	v := s.platSerial
	s.platSerial++
	return v
}

func (s *session) OnFrame(ctx context.Context, f gateway.Frame) error {
	h, body, err := parseHeader(f.Payload)
	if err != nil {
		s.log().Debug(map[string]any{"event": "bad_frame", "error": err.Error()})
		return nil
	}
	// Reassemble subpackaged messages before dispatch. The N62 splits some larger
	// replies (e.g. certain ULV 0xB051 config segments) across frames; without this
	// only the first fragment's body reaches the handler and the JSON fails to parse.
	// 0x0801 image uploads carry their own reassembler, so leave those to it.
	if h.Subpackage && h.SubTotal > 1 && h.MsgID != msgMultimediaData {
		full, complete := s.reassemble(h, body)
		if !complete {
			s.ack(h)
			return nil
		}
		body = full
	}
	switch h.MsgID {
	case msgRegister:
		return s.handleRegister(ctx, h)
	case msgAuth:
		s.ack(h)
		s.authorize(ctx, h)
		return nil
	case msgHeartbeat:
		s.authorize(ctx, h)
		s.ack(h)
		return nil
	case msgLocation:
		s.handleLocation(ctx, h, body)
		s.ack(h)
		return nil
	case msgBatchLocation:
		for _, inner := range splitBatch(body) {
			s.handleLocation(ctx, h, inner)
		}
		s.ack(h)
		return nil
	case msgTermGeneralResp:
		// Device ack to a platform command (video start/stop, playback). Correlate
		// by the acked message id so the right waiter is released. No reply to an ack.
		ack := parseTermGeneralResp(body)
		s.log().Debug(map[string]any{"event": "device_ack", "serial": s.serial, "ack_msg_id": fmt.Sprintf("0x%04x", ack["ack_msg_id"]), "result": ack["result"]})
		if id, ok := ack["ack_msg_id"].(int); ok {
			s.deliverPending(id, ack)
		}
		return nil
	case msgResourceList:
		// Unsolicited recording-list reply to a 0x9205 query; no platform ack.
		recs := parseResourceList(body, s.tzOffset())
		s.deliverPending(msgResourceList, map[string]any{"recordings": recs})
		return nil
	case msgTermAttrs:
		s.deliverPending(msgTermAttrs, parseTermAttrs(body))
		s.ack(h)
		return nil
	case msgVehicleInfo:
		s.deliverPending(msgVehicleInfo, parseVehicleInfo(body))
		s.ack(h)
		return nil
	case msgUlvParamResp:
		s.deliverPending(msgUlvParamResp, parseUlvParamResp(body))
		s.ack(h)
		return nil
	case msgTransparentUp:
		s.handleTransparent(body)
		s.ack(h)
		return nil
	case msgMultimediaData:
		// Still-image upload (0x0801); reassemble + deliver/save, then ack.
		s.handleMultimediaData(h, body)
		s.ack(h)
		return nil
	case msgMultimediaEvent:
		// Snapshot metadata (0x0800); log and ack (the image follows in 0x0801).
		s.log().Debug(map[string]any{"event": "multimedia_event", "serial": s.serial, "len": len(body)})
		s.ack(h)
		return nil
	case msgCameraShootResp:
		// Camera-shoot command response (0x0805): lists the multimedia ids; log + ack.
		s.log().Debug(map[string]any{"event": "camera_shoot_resp", "serial": s.serial, "len": len(body)})
		s.ack(h)
		return nil
	case msgDriverCard, msgPassengerCount, msgFileUploadComplete:
		// Acknowledge to stop the device's retry loop; payloads not yet parsed.
		s.ack(h)
		return nil
	default:
		s.log().Debug(map[string]any{"event": "unsupported_message", "msg_id": fmt.Sprintf("0x%04x", h.MsgID), "serial": s.serial})
		return nil
	}
}

// ack sends a 0x8001 platform general response for a terminal message.
func (s *session) ack(h header) {
	_ = s.conn.WriteFrame(buildGeneralResp(h.Phone, s.nextPlatSerial(), h.Serial, h.MsgID, resultOK))
}

// handleRegister authorizes the device and replies 0x8100 (+ a compat 0x8001).
func (s *session) handleRegister(ctx context.Context, h header) error {
	known := s.authorize(ctx, h)
	result := byte(resultOK)
	if !known {
		result = 1 // registration failed: not admitted
	}
	_ = s.conn.WriteFrame(buildRegisterResp(h.Phone, s.nextPlatSerial(), h.Serial, result, authCode(h.Phone)))
	// Some firmware also expects a general-response ack to the 0x0100.
	s.ack(h)
	return nil
}

// authorize derives the serial from the header phone (once), runs the device
// authenticator, and on success marks the session online and registers it in the
// Hub. Returns whether the device is admitted. Idempotent.
func (s *session) authorize(ctx context.Context, h header) bool {
	if s.serial == "" {
		s.phone = h.Phone
		s.serial = device.NormalizeSerial(serialFromPhone(h.Phone))
	}
	if s.approved {
		return true
	}
	res, err := s.conn.Deps.Auth.Authorize(ctx, device.RegisterInfo{
		Serial:   s.serial,
		Protocol: s.proto.cfg.Unit,
		RemoteIP: s.conn.RemoteIP(),
		Meta:     map[string]any{"phone": h.Phone, "model": s.model},
	})
	if err != nil {
		s.log().Error(map[string]any{"event": "device_gate_error", "serial": s.serial, "error": err.Error()})
		return false
	}
	if !res.Known {
		s.log().Info(map[string]any{"event": "unknown_device_quarantined", "serial": s.serial})
		return false
	}
	s.approved = true
	s.lifecycle = "online"
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "online"); err != nil {
		s.log().Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "online", "error": err.Error()})
	}
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Register(gateway.DeviceInfo{
			Serial:      s.serial,
			Protocol:    s.proto.cfg.Unit,
			Model:       s.model,
			RemoteAddr:  s.conn.RemoteAddr().String(),
			ConnectedAt: time.Now().UTC(),
			State:       "online",
		}, s)
	}
	s.log().Info(map[string]any{"event": "device_approved", "serial": s.serial, "model": s.model})
	return true
}

// handleLocation parses a 0x0200 body and emits gps/event telemetry.
func (s *session) handleLocation(ctx context.Context, h header, body []byte) {
	s.authorize(ctx, h)
	if !s.approved {
		return
	}
	loc, ok := parseLocation(body, s.tzOffset())
	if !ok {
		s.log().Debug(map[string]any{"event": "bad_location", "serial": s.serial, "len": len(body)})
		return
	}
	payload, isEvent := s.proto.buildLocationPayload(loc, s.model)
	kind := "gps"
	if isEvent {
		kind = "event"
	}
	// Decode trace for the live Mapping Test. trace has an entry for every raw alarm
	// signal present (mapped or not), so it's non-empty whenever the device is
	// signalling an alarm — even one that maps to nothing yet (surfaced as unmapped).
	_, trace := s.proto.resolveEventsTrace(loc, s.model)
	if len(trace) > 0 {
		// event_forward (the name the Mapping Test matches) carries the trace; logged
		// at Info so it's visible without per-frame GPS debug noise.
		s.log().Info(map[string]any{
			"event": "event_forward", "serial": s.serial, "kind": kind, "model": s.model,
			"lat": loc.Latitude, "lon": loc.Longitude, "speed_kmh": loc.Speed,
			"bearing": loc.Direction, "utc": loc.TimeUTC, "alarm": loc.Alarm,
			"mapped_events": payload["event"], "mapping_trace": trace,
		})
	} else {
		s.log().Debug(map[string]any{
			"event": "location_forward", "serial": s.serial, "kind": kind,
			"lat": loc.Latitude, "lon": loc.Longitude, "speed_kmh": loc.Speed,
			"bearing": loc.Direction, "utc": loc.TimeUTC,
		})
	}
	s.conn.Emit(s.serial, s.proto.cfg.Make, s.model, kind, payload)
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
			s.log().Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "offline", "error": err.Error()})
		}
	}
}

// ---- command/response correlation ----

func (s *session) registerPending(key int) (chan map[string]any, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, exists := s.pending[key]; exists {
		return nil, fmt.Errorf("another request of this type is in progress")
	}
	ch := make(chan map[string]any, 1)
	s.pending[key] = ch
	return ch, nil
}

func (s *session) clearPending(key int) {
	s.pendingMu.Lock()
	delete(s.pending, key)
	s.pendingMu.Unlock()
}

// deliverPending hands a parsed response to a waiter, if one is registered.
func (s *session) deliverPending(key int, data map[string]any) {
	s.pendingMu.Lock()
	ch := s.pending[key]
	if ch != nil {
		delete(s.pending, key)
	}
	s.pendingMu.Unlock()
	if ch != nil {
		ch <- data
	}
}

// request sends a platform message (built by send) and waits for the device
// reply correlated by key.
func (s *session) request(ctx context.Context, key int, frame []byte) (map[string]any, error) {
	ch, err := s.registerPending(key)
	if err != nil {
		return nil, err
	}
	defer s.clearPending(key)
	if err := s.conn.WriteFrame(frame); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case resp := <-ch:
		return resp, nil
	}
}

// ---- Commander ----

func (s *session) SupportedCommands() []string {
	return []string{"reboot_unit", "request_environment", "request_vehicle_info", "request_basic_status", "stop_playback"}
}

func (s *session) SendCommand(ctx context.Context, cmd gateway.Command) (gateway.CommandResult, error) {
	if s.serial == "" || !s.approved {
		return gateway.CommandResult{}, gateway.ErrNotConnected
	}
	now := func() time.Time { return time.Now().UTC() }
	switch cmd.Type {
	case "reboot_unit":
		// 0x8105 terminal control, command word 0x74 (reboot). The device reboots
		// and won't reliably answer, so report acceptance.
		if err := s.conn.WriteFrame(buildFrame(msgTerminalControl, s.phone, s.nextPlatSerial(), []byte{0x74})); err != nil {
			return gateway.CommandResult{}, err
		}
		return gateway.CommandResult{Data: map[string]any{"ok": true}, ReceivedAt: now()}, nil

	case "request_environment":
		frame := buildFrame(msgQueryTermAttrs, s.phone, s.nextPlatSerial(), nil)
		data, err := s.request(ctx, msgTermAttrs, frame)
		if err != nil {
			return gateway.CommandResult{}, err
		}
		return gateway.CommandResult{Data: data, ReceivedAt: now()}, nil

	case "request_vehicle_info":
		frame := buildFrame(msgVehicleInfoQuery, s.phone, s.nextPlatSerial(), nil)
		data, err := s.request(ctx, msgVehicleInfo, frame)
		if err != nil {
			return gateway.CommandResult{}, err
		}
		return gateway.CommandResult{Data: data, ReceivedAt: now()}, nil

	case "request_basic_status":
		return s.requestBasicStatus(ctx, cmd)

	case "stop_playback":
		camera := 0
		if cmd.Payload != nil {
			if v, ok := cmd.Payload["camera"].(float64); ok {
				camera = int(v)
			}
		}
		if err := s.stopPlayback(camera); err != nil {
			return gateway.CommandResult{}, err
		}
		return gateway.CommandResult{Data: map[string]any{"ok": true}, ReceivedAt: now()}, nil

	default:
		return gateway.CommandResult{}, gateway.ErrUnsupportedCommand
	}
}
