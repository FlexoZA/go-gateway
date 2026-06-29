// Package navtelecom implements Navtelecom NTCB/FLEX GPS trackers (e.g. the
// START S-2011) as a gateway plugin. It is a GPS-only unit: devices dial out
// over TCP and speak NTCB (binary transport + commands) carrying a FLEX
// telemetry stream. The session runs the handshake, then decodes FLEX telemetry
// records (mask-driven) and forwards a normalized payload via conn.Emit — the
// universal webhook handles the rest. No video, and (for now) no commands/config.
//
// See docs/navtelecom-integration-plan.md for the protocol overview and codec.go
// for the wire format.
package navtelecom

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
)

const (
	unitName    = "navtelecom"
	deviceMake  = "navtelecom"
	deviceModel = "START S-2011"

	// maxFlexBody caps a single FLEX telemetry payload (records + framing). The
	// spec keeps array packets under ~1.3 KB; this is a generous sanity bound.
	maxFlexBody = 64 * 1024

	// idleTimeout is the per-connection read deadline. NTCB devices ping/telemeter
	// infrequently; match Fleetiger's relaxed window rather than the 3m default.
	idleTimeout = 6 * time.Minute
)

// Protocol is the Navtelecom unit-type plugin.
//
// recordLens holds, per live connection, the negotiated FLEX record length so
// the otherwise-stateless ReadFrame can frame `~A`/`~T`/`~C` messages (whose
// length depends on the negotiated mask). It is keyed by the connection's
// *bufio.Reader — the only handle ReadFrame is given — set when ReadFrame frames
// the self-delimiting `*>FLEX` packet, and deleted when the read loop ends
// (ReadFrame returns an error). The session parses the mask independently for
// decoding, so this map only ever carries an int.
type Protocol struct {
	recordLens sync.Map // map[*bufio.Reader]int
}

// New returns a Navtelecom protocol plugin.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return unitName }

// Capabilities: a GPS-only tracker declares nothing optional, so no video,
// command, or config code is wired in. Config (NTCB *!READ/*!EDITS) and commands
// (outputs/reboot) are deliberately deferred to later milestones.
func (*Protocol) Capabilities() gateway.Capabilities { return gateway.Capabilities{} }

// IdleTimeout accommodates the device's infrequent transmissions.
func (*Protocol) IdleTimeout() time.Duration { return idleTimeout }

// DefaultDevicePort is the port Navtelecom devices dial when no NAVTELECOM_PORT
// override is set, letting this unit be hosted alongside others in one process.
func (*Protocol) DefaultDevicePort() int { return 4000 }

func (p *Protocol) setRecordLen(r *bufio.Reader, n int) { p.recordLens.Store(r, n) }
func (p *Protocol) delRecordLen(r *bufio.Reader)        { p.recordLens.Delete(r) }
func (p *Protocol) recordLen(r *bufio.Reader) int {
	if v, ok := p.recordLens.Load(r); ok {
		return v.(int)
	}
	return 0
}

// ReadFrame decodes exactly one message from the stream, dispatching on the
// first byte: '@' NTCB packet (16-byte header + body), '~' FLEX message, or 0x7F
// FLEX ping. On any read error it clears this connection's cached record length
// and returns the error (the read loop then ends).
func (p *Protocol) ReadFrame(r *bufio.Reader) (gateway.Frame, error) {
	marker, err := r.Peek(1)
	if err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	switch marker[0] {
	case markerPing:
		if _, err := r.ReadByte(); err != nil {
			p.delRecordLen(r)
			return gateway.Frame{}, err
		}
		return gateway.Frame{Type: markerPing}, nil
	case markerNTCB:
		return p.readNTCB(r)
	case markerFLEX:
		return p.readFLEX(r)
	default:
		p.delRecordLen(r)
		return gateway.Frame{}, fmt.Errorf("navtelecom: unexpected leading byte %#x", marker[0])
	}
}

// readNTCB reads one NTCB packet (16-byte header + body). When the body is a
// `*>FLEX` negotiation it parses the mask and caches the record length for
// subsequent FLEX framing on this connection (ignoring a parse error — the
// session forces a 1.0 renegotiation in that case).
func (p *Protocol) readNTCB(r *bufio.Reader) (gateway.Frame, error) {
	head := make([]byte, ntcbHeaderLen)
	if _, err := io.ReadFull(r, head); err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	hdr, err := parseNTCBHeader(head)
	if err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	if hdr.BodyLen > maxFlexBody {
		p.delRecordLen(r)
		return gateway.Frame{}, fmt.Errorf("navtelecom: oversized NTCB body %d", hdr.BodyLen)
	}
	packet := make([]byte, ntcbHeaderLen+hdr.BodyLen)
	copy(packet, head)
	if _, err := io.ReadFull(r, packet[ntcbHeaderLen:]); err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	body := packet[ntcbHeaderLen:]
	if _, _, mask, err := parseFlexNegotiation(body); err == nil {
		p.setRecordLen(r, mask.recordLen)
	}
	return gateway.Frame{Type: markerNTCB, Payload: packet}, nil
}

// readFLEX reads one raw FLEX telemetry message using this connection's cached
// record length. `~E`/`~X` (FLEX 2.0 additional packets) are unsupported here —
// we cap negotiation at 1.0 so they should never arrive.
func (p *Protocol) readFLEX(r *bufio.Reader) (gateway.Frame, error) {
	head := make([]byte, 2) // '~' + type
	if _, err := io.ReadFull(r, head); err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	recLen := p.recordLen(r)
	readBody := func(n int) ([]byte, error) {
		if n < 0 || n > maxFlexBody {
			return nil, fmt.Errorf("navtelecom: FLEX body length %d out of range", n)
		}
		buf := make([]byte, n)
		_, err := io.ReadFull(r, buf)
		return buf, err
	}

	var rest []byte
	var err error
	switch head[1] {
	case flexArray:
		if recLen == 0 {
			err = fmt.Errorf("navtelecom: ~A before FLEX negotiation")
			break
		}
		var size byte
		if size, err = r.ReadByte(); err != nil {
			break
		}
		// size byte already read; records + 1-byte CRC8 follow.
		var bodyBytes []byte
		if bodyBytes, err = readBody(int(size)*recLen + 1); err == nil {
			rest = append([]byte{size}, bodyBytes...)
		}
	case flexCurrent:
		if recLen == 0 {
			err = fmt.Errorf("navtelecom: ~C before FLEX negotiation")
			break
		}
		rest, err = readBody(recLen + 1) // record + CRC8
	case flexOutOrder:
		if recLen == 0 {
			err = fmt.Errorf("navtelecom: ~T before FLEX negotiation")
			break
		}
		rest, err = readBody(4 + recLen + 1) // eventindex(U32) + record + CRC8
	default:
		err = fmt.Errorf("navtelecom: unsupported FLEX message %q", head[1])
	}
	if err != nil {
		p.delRecordLen(r)
		return gateway.Frame{}, err
	}
	msg := append(head, rest...)
	return gateway.Frame{Type: markerFLEX, Payload: msg}, nil
}

func (p *Protocol) NewSession(c *gateway.Conn) gateway.Session {
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
	serial string // device IMEI, from the *>S handshake
	gate   gateStatus

	deviceID uint32 // NTCB sender id of the device (for reply addressing)
	serverID uint32 // NTCB id the device addressed us as

	mask           flexMask
	flexNegotiated bool
}

// OnFrame dispatches one decoded message.
func (s *session) OnFrame(ctx context.Context, f gateway.Frame) error {
	switch f.Type {
	case markerPing:
		return nil // keep-alive; no response required
	case markerNTCB:
		return s.onNTCB(ctx, f.Payload)
	case markerFLEX:
		return s.onFLEX(f.Payload)
	default:
		return nil
	}
}

// onNTCB handles an NTCB packet: the *>S identity handshake and *>FLEX
// negotiation. Other NTCB command bodies are logged and ignored.
func (s *session) onNTCB(ctx context.Context, packet []byte) error {
	log := s.conn.Deps.Log.With("tcp/navtelecom")
	hdr, err := parseNTCBHeader(packet)
	if err != nil {
		log.Debug(map[string]any{"event": "ntcb_header_error", "error": err.Error()})
		return nil
	}
	body := packet[ntcbHeaderLen:]
	if len(packet) >= 15 && xorSum(body) != packet[14] {
		log.Debug(map[string]any{"event": "ntcb_body_checksum_mismatch", "serial": s.serialOrUnknown()})
		return nil
	}
	// Remember addressing so replies can swap recipient/sender.
	s.deviceID, s.serverID = hdr.SenderID, hdr.RecipientID

	switch {
	case hasPrefix(body, "*>S"):
		return s.handleIdentity(ctx, body)
	case hasPrefix(body, "*>FLEX"):
		return s.handleFlexNegotiation(body)
	default:
		log.Debug(map[string]any{"event": "ntcb_unhandled", "serial": s.serialOrUnknown(), "body": printable(body)})
		return nil
	}
}

// handleIdentity processes `*>S:<id-string>`: extracts the IMEI, authorizes the
// device, and replies `*<S`. An unknown device is rejected (returns an error,
// closing the connection) — this happens before FLEX negotiation, so no per-conn
// record length has been cached.
func (s *session) handleIdentity(ctx context.Context, body []byte) error {
	log := s.conn.Deps.Log.With("tcp/navtelecom")
	imei := imeiFromIDString(body)
	if imei == "" {
		log.Debug(map[string]any{"event": "bad_id_string", "body": printable(body)})
		return fmt.Errorf("navtelecom: unparseable *>S id string")
	}
	s.serial = device.NormalizeSerial(imei)

	info := device.RegisterInfo{
		Serial:   s.serial,
		Protocol: unitName,
		RemoteIP: s.conn.RemoteIP(),
		Meta:     map[string]any{"message_type": "identity"},
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
	log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "protocol": result.Protocol})

	// Reply `*<S` (acknowledge the identity), addressed back to the device.
	if err := s.conn.WriteFrame(buildNTCB(s.deviceID, s.serverID, []byte{'*', '<', 'S'})); err != nil {
		return nil // socket dying; let the next read end the connection (and clean up)
	}
	return nil
}

// handleFlexNegotiation parses the device's `*>FLEX` mask and replies `*<FLEX`
// capping the version at 1.0 (forcing the device onto the stable 1.0 fields and
// no additional packets). It then registers the device with the hub so the HTTP
// API can list it.
func (s *session) handleFlexNegotiation(body []byte) error {
	log := s.conn.Deps.Log.With("tcp/navtelecom")
	if s.gate != gateApproved {
		log.Debug(map[string]any{"event": "flex_before_identity"})
		return nil
	}
	protoVer, structVer, mask, err := parseFlexNegotiation(body)
	if err != nil {
		// Mask we can't frame (e.g. fields beyond what we decode). Reply 1.0 to
		// force a renegotiation down to the stable field set.
		log.Debug(map[string]any{"event": "flex_mask_unsupported", "serial": s.serial, "error": err.Error()})
		s.flexNegotiated = false
		return s.replyFlex(flexVer10, flexVer10)
	}
	s.mask = mask
	s.flexNegotiated = true
	log.Info(map[string]any{
		"event": "flex_negotiated", "serial": s.serial,
		"proto_ver": protoVer, "struct_ver": structVer,
		"fields": len(mask.fields), "record_len": mask.recordLen,
	})
	if err := s.replyFlex(capVersion(protoVer), capVersion(structVer)); err != nil {
		return nil
	}
	s.registerHub()
	return nil
}

// replyFlex sends `*<FLEX<proto><proto_ver><struct_ver>` as an NTCB packet.
func (s *session) replyFlex(protoVer, structVer byte) error {
	body := []byte{'*', '<', 'F', 'L', 'E', 'X', flexProtocol, protoVer, structVer}
	return s.conn.WriteFrame(buildNTCB(s.deviceID, s.serverID, body))
}

// onFLEX verifies a FLEX telemetry message's CRC8, decodes its record(s),
// forwards GPS telemetry, and sends the mandatory server acknowledgement. A bad
// CRC is dropped WITHOUT an ACK (per spec the device then retransmits).
func (s *session) onFLEX(msg []byte) error {
	log := s.conn.Deps.Log.With("tcp/navtelecom")
	if len(msg) < 3 {
		return nil
	}
	if got, want := msg[len(msg)-1], crc8(msg[:len(msg)-1]); got != want {
		log.Debug(map[string]any{"event": "flex_crc_mismatch", "serial": s.serialOrUnknown(), "got": got, "want": want})
		return nil
	}
	if !s.flexNegotiated {
		log.Debug(map[string]any{"event": "flex_before_negotiation", "serial": s.serialOrUnknown()})
		return nil
	}
	body := msg[2 : len(msg)-1] // strip '~<type>' marker and trailing CRC8

	switch msg[1] {
	case flexArray:
		// body = size(1) + size*recordLen records.
		if len(body) < 1 {
			return nil
		}
		size := int(body[0])
		s.decodeRecords(body[1:], size)
		return s.ackArray(byte(size))
	case flexCurrent:
		// body = one record (current state); index 0, event 0xFF00 — ACK only.
		s.decodeRecords(body, 1)
		return s.ackSimple(flexCurrent)
	case flexOutOrder:
		// body = eventindex(U32) + one record. Decode the record (skip the index).
		if len(body) < 4 {
			return nil
		}
		s.decodeRecords(body[4:], 1)
		return s.ackOutOrder(body[0:4])
	default:
		return nil
	}
}

// decodeRecords decodes up to count records of mask.recordLen bytes and emits GPS
// telemetry for those carrying a position. Malformed records are skipped.
func (s *session) decodeRecords(data []byte, count int) {
	log := s.conn.Deps.Log.With("tcp/navtelecom")
	for i := 0; i < count; i++ {
		start := i * s.mask.recordLen
		end := start + s.mask.recordLen
		if end > len(data) {
			log.Debug(map[string]any{"event": "record_truncated", "serial": s.serial, "have": len(data), "want": end})
			return
		}
		rec, err := decodeRecord(s.mask, data[start:end])
		if err != nil {
			log.Debug(map[string]any{"event": "record_decode_error", "serial": s.serial, "error": err.Error()})
			continue
		}
		if s.gate != gateApproved || !rec.HasLat || !rec.HasLon {
			continue // P1: forward only positioned records as GPS; events come later
		}
		payload := buildPayload(s.serial, rec)
		log.Debug(map[string]any{
			"event": "gps_forward", "serial": s.serial,
			"lat": payload["latitude"], "lon": payload["longitude"],
			"speed": payload["speed"], "event_id": rec.EventID,
		})
		s.conn.Emit(s.serial, deviceMake, deviceModel, "gps", payload)
	}
}

// ackArray replies `~A<size><crc8>`.
func (s *session) ackArray(size byte) error {
	out := []byte{markerFLEX, flexArray, size}
	out = append(out, crc8(out))
	return s.conn.WriteFrame(out)
}

// ackSimple replies `~<type><crc8>` (used for ~C).
func (s *session) ackSimple(typ byte) error {
	out := []byte{markerFLEX, typ}
	out = append(out, crc8(out))
	return s.conn.WriteFrame(out)
}

// ackOutOrder replies `~T<eventindex><crc8>`, echoing the record index.
func (s *session) ackOutOrder(eventIndex []byte) error {
	out := append([]byte{markerFLEX, flexOutOrder}, eventIndex...)
	out = append(out, crc8(out))
	return s.conn.WriteFrame(out)
}

// registerHub makes the device listable by the HTTP control API.
func (s *session) registerHub() {
	if s.conn.Deps.Hub == nil {
		return
	}
	s.conn.Deps.Hub.Register(gateway.DeviceInfo{
		Serial:      s.serial,
		Protocol:    unitName,
		Model:       deviceModel,
		RemoteAddr:  s.conn.RemoteAddr().String(),
		ConnectedAt: time.Now().UTC(),
		State:       "online",
	}, s)
}

// OnClose marks the device offline, unless a reconnect already replaced it.
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
			s.conn.Deps.Log.With("tcp/navtelecom").Debug(map[string]any{
				"event": "device_status_update_failed", "serial": s.serial, "status": "offline", "error": err.Error(),
			})
		}
	}
}

// SendCommand satisfies gateway.Commander so the session can register with the
// hub. Navtelecom is GPS-only for now (HasCommands is false), so it supports none.
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

// hasPrefix reports whether body starts with the ASCII prefix.
func hasPrefix(body []byte, prefix string) bool {
	return len(body) >= len(prefix) && string(body[:len(prefix)]) == prefix
}

// imeiFromIDString extracts the IMEI digits from a `*>S:<id-string>` body. The
// id string (15 chars) carries the modem IMEI; we keep the leading digit run.
func imeiFromIDString(body []byte) string {
	const prefix = "*>S:"
	if !hasPrefix(body, prefix) {
		// Some firmwares may omit the ':'; fall back to everything after "*>S".
		if hasPrefix(body, "*>S") {
			return digitsPrefix(string(body[3:]))
		}
		return ""
	}
	return digitsPrefix(string(body[len(prefix):]))
}

// digitsPrefix returns the leading run of ASCII digits in s, trimmed of spaces
// and NUL padding.
func digitsPrefix(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

// printable renders an NTCB command body for logs, keeping ASCII and hex-escaping
// the rest.
func printable(body []byte) string {
	var b strings.Builder
	for _, c := range body {
		if c >= 0x20 && c < 0x7f {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\x%02x", c)
		}
	}
	return b.String()
}
