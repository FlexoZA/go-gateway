// Package jt808 implements the JT/T 808-2019 unit type for the N62 fleet dashcam
// as a gateway plugin: registration/auth, GPS + event telemetry to the universal
// webhook, device parameter config (ConfigController over ULV 0xB050/0xB051), a
// live status snapshot (StatusReporter from ULV transparent 0x0900), and control
// commands (Commander). Live video / clips / snapshots arrive in a later phase
// over a separate media port (see docs/jt808-integration-plan.md).
//
// Wire format and command set are ported from the old Node gateway
// (dfm-mvr-gateway/src/tcp/jt808Server.js), validated against this exact device.
package jt808

import (
	"bufio"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

const (
	unitName    = "jt808"    // registry protocol + log namespace
	deviceMake  = "jt808_19" // universal-message make (matches the builder's jt808Switch)
	deviceModel = "N62"

	defaultControlPort = 6608
	defaultMediaPort   = 6609
	idleTimeout        = 6 * time.Minute

	logNS = "tcp/jt808"
)

// Protocol is the JT808 unit-type plugin. routes is shared between control
// sessions (which start streams/clips) and the media listener (which routes the
// device's JT1078 connections); see media.go.
type Protocol struct {
	routes *streamRoutes
}

// New returns a JT808 protocol plugin.
func New() *Protocol { return &Protocol{routes: newStreamRoutes()} }

func (*Protocol) Name() string { return unitName }

func (*Protocol) Capabilities() gateway.Capabilities {
	// HasSnapshots is false: the validated N62 firmware ignores the on-demand
	// camera-shoot command (0x8801) — it sends no response at all — so the capture
	// UI would always fail. The Snapshotter methods and the auto-push 0x0801
	// handler remain (stills the device pushes on events/interval are still
	// reassembled and saved); flip this to true for firmware that supports 0x8801.
	return gateway.Capabilities{HasVideo: true, HasCommands: true, HasConfig: true, HasStatus: true}
}

// DefaultDevicePort is the JT808 control port the N62 dials (also where it sends
// JT1078 video unless we advertise the media port — which we do).
func (*Protocol) DefaultDevicePort() int { return defaultControlPort }

// DefaultMediaPort keeps the JT808 media listener (added with video) off the
// other video units' ports when hosted in one process.
func (*Protocol) DefaultMediaPort() int { return defaultMediaPort }

// IdleTimeout widens the read deadline: the N62 heartbeats/reports periodically
// but can be quiet between fixes.
func (*Protocol) IdleTimeout() time.Duration { return idleTimeout }

// MappingProvider: JT808 event output is driven by editable alarm-bit / ULV /
// vendor-TLV → event-code tables (see events.go).
func (*Protocol) DefaultMappingEntries() []mapping.Entry { return DefaultMappingEntries() }
func (*Protocol) ApplyMappings(byModel mapping.ByModel)  { ApplyMappings(byModel) }

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
	return &session{conn: c, routes: p.routes, pending: map[int]chan map[string]any{}}
}

type session struct {
	conn   *gateway.Conn
	routes *streamRoutes
	serial string // gateway serial "JT808_<digits>"
	phone  string // raw terminal-phone digits (for addressing replies)
	model  string // device model ("N62")

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
}

func (s *session) log() *logging.Logger { return s.conn.Deps.Log.With(logNS) }

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
		recs := parseResourceList(body, s.conn.Deps.DeviceTZOffsetHours)
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
		s.model = deviceModel
	}
	if s.approved {
		return true
	}
	res, err := s.conn.Deps.Auth.Authorize(ctx, device.RegisterInfo{
		Serial:   s.serial,
		Protocol: unitName,
		RemoteIP: s.conn.RemoteIP(),
		Meta:     map[string]any{"phone": h.Phone, "model": deviceModel},
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
			Protocol:    unitName,
			Model:       deviceModel,
			RemoteAddr:  s.conn.RemoteAddr().String(),
			ConnectedAt: time.Now().UTC(),
			State:       "online",
		}, s)
	}
	s.log().Info(map[string]any{"event": "device_approved", "serial": s.serial, "model": deviceModel})
	return true
}

// handleLocation parses a 0x0200 body and emits gps/event telemetry.
func (s *session) handleLocation(ctx context.Context, h header, body []byte) {
	s.authorize(ctx, h)
	if !s.approved {
		return
	}
	loc, ok := parseLocation(body, s.conn.Deps.DeviceTZOffsetHours)
	if !ok {
		s.log().Debug(map[string]any{"event": "bad_location", "serial": s.serial, "len": len(body)})
		return
	}
	payload, isEvent := buildLocationPayload(loc, s.model)
	kind := "gps"
	if isEvent {
		kind = "event"
	}
	fields := map[string]any{
		"event": "location_forward", "serial": s.serial, "kind": kind,
		"lat": loc.Latitude, "lon": loc.Longitude, "speed_kmh": loc.Speed,
		"bearing": loc.Direction, "utc": loc.TimeUTC, "alarm": loc.Alarm,
		"mapped_events": payload["event"],
	}
	// Events log at Info so they're visible without per-frame GPS debug noise;
	// plain GPS stays at Debug.
	if isEvent {
		s.log().Info(fields)
	} else {
		s.log().Debug(fields)
	}
	s.conn.Emit(s.serial, deviceMake, s.model, kind, payload)
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
