// Package fleetiger implements the FleeTiger (Concox GT06) GPS tracker as a
// gateway plugin. It is a GPS-only unit: devices dial out over TCP and stream
// 0x78 0x78-framed binary packets (login, location, heartbeat, alarm). The
// session decodes them and forwards a normalized payload via conn.Emit — the
// universal webhook handles the rest. No video, commands, or config.
//
// Ported from the original JS gateway's src/tcp/gt06Server.js. See codec.go for
// the wire format and docs/fleetiger/GT06 for the protocol spec.
package fleetiger

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/flow"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

// settingTimezoneOffset is the unit-settings key for the device's local-clock
// offset from UTC (GT06 sends wall-clock with no timezone).
const settingTimezoneOffset = "timezone_offset_hours"

const (
	unitName    = "fleetiger"
	deviceMake  = "fleetiger"
	deviceModel = "GT06"

	// maxPacketBytes caps a single framed packet. The length byte is one octet so
	// a packet can never exceed 0xFF+5; this is a generous sanity bound.
	maxPacketBytes = 1024

	// idleTimeout is the per-connection read deadline. GT06 devices heartbeat
	// roughly every 3 minutes, so the framework default (3m) is too tight — a
	// parked unit that only heartbeats would be disconnected on timing jitter.
	idleTimeout = 6 * time.Minute
)

// Protocol is the FleeTiger unit-type plugin.
type Protocol struct{}

// New returns a FleeTiger protocol plugin.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return unitName }

// Capabilities: a GPS-only tracker declares nothing optional, so no video,
// command, or config code is wired in.
func (*Protocol) Capabilities() gateway.Capabilities { return gateway.Capabilities{} }

// IdleTimeout overrides the framework default to accommodate the device's slow
// (~3 minute) heartbeat cadence. Detected by the app runner via type assertion.
func (*Protocol) IdleTimeout() time.Duration { return idleTimeout }

// DefaultDevicePort is the port FleeTiger devices dial when no <UNIT>_PORT
// override is set. Lets one gateway process host this unit alongside others.
func (*Protocol) DefaultDevicePort() int { return 8050 }

// SettingsSchema declares the unit's editable gateway-side settings (rendered as
// FleeTiger's settings screen). Implements gateway.ConfigurableUnit.
func (*Protocol) SettingsSchema() []gateway.SettingField {
	return []gateway.SettingField{{
		Key:     settingTimezoneOffset,
		Label:   "Device timezone offset (hours)",
		Type:    "number",
		Default: "0",
		Help:    "GT06 sends local wall-clock with no timezone. Set e.g. 2 for SAST, or 0 if the unit already sends UTC.",
		Group:   "Time",
	}}
}

// MappingProvider: the provisional alarm/language code → event mapping is editable
// from the admin (map type "alarm_former"). These thin methods let the app runner
// seed and apply mappings without importing this package; they delegate to the
// package-level mapping state. FleeTiger has no per-model workflows.
func (*Protocol) DefaultMappingEntries() []mapping.Entry  { return DefaultMappingEntries() }
func (*Protocol) ApplyMappings(t mapping.Table)           { ApplyMappings(t) }
func (*Protocol) ApplyWorkflows(_ map[string]*flow.Graph) {}
func (*Protocol) WorkflowModelCount() int                 { return 0 }

// ReadFrame decodes exactly one GT06 frame from the stream. It first resyncs to
// the 0x78 0x78 start marker (discarding any inter-frame noise), reads the length
// byte, then the remainder of the packet. A bad CRC is NOT a framing error — it is
// validated in the session so a single corrupt packet does not drop the
// connection; only a structurally broken frame (bad terminator/oversize) does.
func (*Protocol) ReadFrame(r *bufio.Reader) (gateway.Frame, error) {
	if err := syncToStart(r); err != nil {
		return gateway.Frame{}, err
	}
	lengthByte, err := r.ReadByte()
	if err != nil {
		return gateway.Frame{}, err
	}
	total := int(lengthByte) + 5
	if total > maxPacketBytes {
		return gateway.Frame{}, fmt.Errorf("fleetiger: oversized packet length %d", total)
	}
	// start(2) + length(1) are consumed; read the remaining total-3 bytes.
	rest := make([]byte, total-3)
	if _, err := io.ReadFull(r, rest); err != nil {
		return gateway.Frame{}, err
	}
	packet := make([]byte, 0, total)
	packet = append(packet, 0x78, 0x78, lengthByte)
	packet = append(packet, rest...)
	if packet[total-2] != 0x0d || packet[total-1] != 0x0a {
		return gateway.Frame{}, fmt.Errorf("fleetiger: bad frame terminator")
	}
	return gateway.Frame{Type: int(packet[3]), Payload: packet}, nil
}

// syncToStart consumes bytes until the 0x78 0x78 start marker has been read,
// leaving the reader positioned at the length byte.
func syncToStart(r *bufio.Reader) error {
	var prev byte
	have := false
	for {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		if have && prev == 0x78 && b == 0x78 {
			return nil
		}
		prev = b
		have = true
	}
}

func (*Protocol) NewSession(c *gateway.Conn) gateway.Session {
	return &session{conn: c}
}

type gateStatus int

const (
	gateNew gateStatus = iota
	gateApproved
	gateQuarantined
)

type session struct {
	conn   *gateway.Conn
	serial string // the device IMEI, established by the login packet
	gate   gateStatus
}

// OnFrame parses a frame, ACKs it when the protocol requires, and forwards
// location/alarm telemetry once the device is approved.
func (s *session) OnFrame(ctx context.Context, f gateway.Frame) error {
	log := s.conn.Deps.Log.With("tcp/fleetiger")

	// The device's local-clock offset comes from the editable unit settings, with
	// the env-configured DeviceTZOffsetHours as the fallback (and for tests, which
	// build Deps without a settings holder).
	tz := s.conn.Deps.Config.DeviceTZOffsetHours
	if us := s.conn.Deps.UnitSettings; us != nil {
		tz = us.Float(settingTimezoneOffset, tz)
	}
	parsed, err := parseGt06Packet(f.Payload, tz)
	if err != nil {
		log.Debug(map[string]any{"event": "packet_parse_error", "remote": s.conn.RemoteAddr().String(), "error": err.Error()})
		return nil // drop the packet, keep the connection
	}
	// Frame-level visibility for the live log: every structurally-valid frame, its
	// protocol number, content length, and CRC result. Lets us see exactly what a
	// device transmits (incl. unhandled/extended GT06 variants).
	log.Debug(map[string]any{"event": "frame", "serial": s.serialOrUnknown(), "protocol": parsed.Protocol, "len": len(parsed.Content), "crc_ok": parsed.CRCValid})

	if !parsed.CRCValid {
		log.Debug(map[string]any{"event": "crc_mismatch", "serial": s.serialOrUnknown(), "protocol": parsed.Protocol})
		return nil // CRC failures are discarded per spec §4.6
	}

	// Identity is established by the login packet and reused for the connection.
	if parsed.Protocol == protoLogin && parsed.IMEI != "" {
		s.serial = device.NormalizeSerial(parsed.IMEI)
	}

	// Acknowledge login/heartbeat/alarm so the device keeps the connection alive.
	if protocolNeedsAck(parsed.Protocol) {
		if err := s.conn.WriteFrame(buildResponse(parsed.Protocol, parsed.SerialNo)); err != nil {
			return err
		}
		log.Debug(map[string]any{"event": "ack_sent", "serial": s.serialOrUnknown(), "protocol": parsed.Protocol, "serial_no": parsed.SerialNo})
	}

	if s.serial == "" {
		// A location/status packet arrived before any login on this connection.
		log.Debug(map[string]any{"event": "packet_before_login", "protocol": parsed.Protocol, "remote": s.conn.RemoteAddr().String()})
		return nil
	}

	switch parsed.Protocol {
	case protoLogin:
		// Login carries no position to forward; it triggers the device gate check.
		return s.handleLogin(ctx)
	case protoLocation:
		if s.gate == gateApproved && parsed.GPS != nil {
			log.Debug(map[string]any{
				"event": "gps_forward", "serial": s.serial,
				"lat": parsed.GPS.Latitude, "lon": parsed.GPS.Longitude,
				"speed": parsed.GPS.Speed, "sats": parsed.GPS.Satellites, "positioning": parsed.GPS.Positioning,
			})
			s.conn.Emit(s.serial, deviceMake, deviceModel, "gps", buildLocationPayload(s.serial, parsed))
		}
	case protoAlarm:
		if s.gate == gateApproved && parsed.GPS != nil {
			log.Debug(map[string]any{
				"event": "event_forward", "serial": s.serial,
				"lat": parsed.GPS.Latitude, "lon": parsed.GPS.Longitude, "events": parsed.Events,
			})
			s.conn.Emit(s.serial, deviceMake, deviceModel, "event", buildLocationPayload(s.serial, parsed))
		}
	case protoStatus:
		// Heartbeat: carries no position (nothing to forward), but surfaces the
		// device's status — useful in the live log. ignition=0 means ACC off, which
		// is why many GT06 units send no location until they move.
		if si := parsed.StatusInfo; si != nil {
			log.Debug(map[string]any{
				"event": "heartbeat", "serial": s.serial,
				"ignition": si.Ignition, "voltage_level": si.VoltageLevel,
				"gsm_signal": si.GSMSignal, "charging": si.Charging,
			})
		}
	default:
		// Not login/location/heartbeat/alarm — e.g. an extended GT06 location
		// variant (0x22/0x26/…) this parser doesn't decode yet. Dump the raw frame
		// so it can be identified and a parse branch added.
		log.Debug(map[string]any{"event": "unhandled_protocol", "serial": s.serialOrUnknown(), "protocol": parsed.Protocol, "hex": fmt.Sprintf("%x", f.Payload)})
	}
	return nil
}

// handleLogin authorizes the device, records it online, and registers it with the
// hub so the HTTP API can list it. An unknown device is rejected by returning an
// error, which closes the connection.
func (s *session) handleLogin(ctx context.Context) error {
	log := s.conn.Deps.Log.With("tcp/fleetiger")
	if s.gate == gateApproved {
		return nil // already approved on this connection (re-login)
	}

	info := device.RegisterInfo{
		Serial:   s.serial,
		Protocol: unitName,
		RemoteIP: s.conn.RemoteIP(),
		Meta:     map[string]any{"message_type": "login"},
	}
	result, err := s.conn.Deps.Auth.Authorize(ctx, info)
	if err != nil {
		log.Error(map[string]any{"event": "device_gate_error", "serial": s.serial, "error": err.Error()})
		return fmt.Errorf("device authorize failed: %w", err)
	}
	if !result.Known {
		s.gate = gateQuarantined
		log.Info(map[string]any{"event": "unknown_device_quarantined", "serial": s.serial})
		return fmt.Errorf("unknown device rejected")
	}

	s.gate = gateApproved
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "online"); err != nil {
		log.Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "online", "error": err.Error()})
	}
	// Make the device reachable/listable by the HTTP control API.
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
	log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "protocol": result.Protocol})
	return nil
}

// OnClose marks the device offline, but only if this session is still the live one
// (a reconnect may have already replaced it in the hub).
func (s *session) OnClose(ctx context.Context) {
	if s.serial == "" || s.gate != gateApproved {
		return
	}
	current := true
	if s.conn.Deps.Hub != nil {
		current = s.conn.Deps.Hub.Unregister(s.serial, s)
	}
	if current {
		if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "offline"); err != nil {
			s.conn.Deps.Log.With("tcp/fleetiger").Debug(map[string]any{
				"event": "device_status_update_failed", "serial": s.serial, "status": "offline", "error": err.Error(),
			})
		}
	}
}

// SendCommand satisfies gateway.Commander so the session can register with the hub.
// FleeTiger is GPS-only (Capabilities.HasCommands is false), so it supports none.
func (s *session) SendCommand(context.Context, gateway.Command) (gateway.CommandResult, error) {
	return gateway.CommandResult{}, gateway.ErrUnsupportedCommand
}

// SupportedCommands reports no controllable commands.
func (s *session) SupportedCommands() []string { return nil }

func (s *session) serialOrUnknown() string {
	if s.serial == "" {
		return "unknown"
	}
	return s.serial
}

// buildLocationPayload assembles the normalized GPS/event payload the universal
// message builder understands (see internal/core/message/message.go).
func buildLocationPayload(serial string, p *parsedPacket) map[string]any {
	g := p.GPS
	payload := map[string]any{
		"imei":        serial,
		"latitude":    g.Latitude,
		"longitude":   g.Longitude,
		"speed":       float64(g.Speed),
		"bearing":     float64(g.Bearing),
		"utc":         float64(g.UTC),
		"positioning": float64(g.Positioning),
		"satellites":  float64(g.Satellites),
	}
	if g.HasIgnition {
		payload["ignition"] = float64(g.Ignition)
	}
	if g.LBS != nil {
		payload["lac"] = []any{float64(g.LBS.LAC)}
	}
	if len(p.Events) > 0 {
		events := make([]any, len(p.Events))
		for i, e := range p.Events {
			events[i] = e
		}
		payload["event"] = events
	}
	return payload
}
