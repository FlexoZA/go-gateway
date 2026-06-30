package jt808

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// status.go implements gateway.StatusReporter from the ULV transparent uplink
// (0x0900): basic status (pass-through type 0xF1 — CPU temp, signal, satellites)
// and SD-card health. It also parses the small command-response bodies used by
// the Commander (0x0107 terminal attributes, 0x4040 vehicle info, 0x0001 general
// response). The exact 0x0900 byte offsets are vendor-specific and best-effort;
// they need confirmation against a live N62 capture (see the integration plan).

// transparentPendingKey is the correlation key for an active request_basic_status
// awaiting a 0x0900 of a given pass-through type. It lives above the 16-bit
// message-id keyspace so it can't collide with a plain message-id waiter.
func transparentPendingKey(passThrough byte) int { return 0x09000000 | int(passThrough) }

// Status reports the latest cached basic status + SD health for the detail view.
func (s *session) Status() (map[string]any, bool) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.basicStatus == nil && s.sdHealth == nil {
		return nil, false
	}
	out := map[string]any{}
	if s.basicStatus != nil {
		out["basic"] = s.basicStatus
		out["basic_at"] = s.basicAt.UTC().Format(time.RFC3339)
	}
	if s.sdHealth != nil {
		out["sd_card"] = s.sdHealth
		out["sd_card_at"] = s.sdHealthAt.UTC().Format(time.RFC3339)
	}
	return out, true
}

// handleTransparent decodes a 0x0900 body: byte 0 is the pass-through type, the
// rest is the vendor payload. Parses the types we know, caches them, and delivers
// to an active request_basic_status waiter.
func (s *session) handleTransparent(body []byte) {
	if len(body) < 1 {
		return
	}
	passThrough := body[0]
	payload := body[1:]
	switch passThrough {
	case 0xf1:
		st := parseBasicStatus(payload)
		st["pass_through_type"] = int(passThrough)
		s.statusMu.Lock()
		s.basicStatus = st
		s.basicAt = time.Now()
		s.statusMu.Unlock()
		s.deliverPending(transparentPendingKey(passThrough), st)
	case 0xf3:
		if sd := parseSdHealth(payload); sd != nil {
			s.statusMu.Lock()
			s.sdHealth = sd
			s.sdHealthAt = time.Now()
			s.statusMu.Unlock()
		}
		s.deliverPending(transparentPendingKey(passThrough), map[string]any{})
	default:
		s.log().Debug(map[string]any{"event": "transparent_uplink", "serial": s.serial, "type": int(passThrough), "len": len(payload)})
		s.deliverPending(transparentPendingKey(passThrough), map[string]any{"pass_through_type": int(passThrough), "raw_hex": hex.EncodeToString(payload)})
	}
}

// requestBasicStatus triggers a 0x8900 transparent downlink (default type 0xF1)
// and waits for the matching 0x0900 reply.
func (s *session) requestBasicStatus(ctx context.Context, cmd gateway.Command) (gateway.CommandResult, error) {
	passThrough := byte(0xf1)
	if cmd.Payload != nil {
		if v, ok := cmd.Payload["transparent_type"]; ok {
			if f, ok := v.(float64); ok {
				passThrough = byte(int(f))
			}
		}
	}
	frame := buildFrame(msgTransparentDown, s.phone, s.nextPlatSerial(), []byte{passThrough})
	data, err := s.request(ctx, transparentPendingKey(passThrough), frame)
	if err != nil {
		return gateway.CommandResult{}, err
	}
	return gateway.CommandResult{Data: data, ReceivedAt: time.Now().UTC()}, nil
}

// parseBasicStatus extracts CPU temperature, network signal, and satellite count
// from a pass-through type 0xF1 payload (ULV basic status). Best-effort: missing
// fields are simply omitted. Offsets per jt808Server.js; validate against a live
// capture.
func parseBasicStatus(p []byte) map[string]any {
	out := map[string]any{}
	if len(p) >= 38 {
		out["network_signal"] = int(p[37])
	}
	if len(p) >= 45 {
		raw := int(binary.BigEndian.Uint16(p[43:45]))
		c := float64(raw-400) / 10.0
		if c > -80 && c < 180 { // sanity range
			out["cpu_temp_c"] = c
			out["raw_temp"] = raw
		}
	}
	if len(p) >= 56 {
		out["satellites"] = int(p[55])
	}
	return out
}

// parseSdHealth extracts SD/HDD counts and sizes from a disk-status pass-through
// payload. The disk record is a TLV (item id 7) after a fixed prefix; layouts
// vary by firmware, so this is best-effort and returns nil if nothing parses.
// Validate against a live capture.
func parseSdHealth(p []byte) map[string]any {
	// Scan for the disk-status TLV (id 7) in the trailing TLV region.
	for i := 0; i+2 <= len(p); {
		id := p[i]
		l := int(p[i+1])
		i += 2
		if i+l > len(p) {
			break
		}
		if id == 7 && l >= 1 {
			return parseDiskRecord(p[i : i+l])
		}
		i += l
	}
	return nil
}

// parseDiskRecord decodes a disk-status record: hard-drive count, totals/remaining
// (MB, 4 bytes each), then SD-card count, totals/remaining.
func parseDiskRecord(d []byte) map[string]any {
	read := func(off, n int) []int {
		vals := []int{}
		for k := 0; k < n; k++ {
			o := off + k*4
			if o+4 > len(d) {
				break
			}
			vals = append(vals, int(binary.BigEndian.Uint32(d[o:o+4])))
		}
		return vals
	}
	if len(d) < 1 {
		return nil
	}
	out := map[string]any{}
	hdCount := int(d[0])
	if hdCount > 2 {
		hdCount = 2
	}
	out["hard_drives"] = map[string]any{
		"count":        hdCount,
		"total_mb":     read(1, hdCount),
		"remaining_mb": read(1+8, hdCount),
	}
	sdOff := 1 + 16
	if sdOff < len(d) {
		sdCount := int(d[sdOff])
		if sdCount > 2 {
			sdCount = 2
		}
		out["sd_cards"] = map[string]any{
			"count":        sdCount,
			"total_mb":     read(sdOff+1, sdCount),
			"remaining_mb": read(sdOff+1+8, sdCount),
		}
	}
	return out
}

// parseTermGeneralResp decodes a 0x0001 terminal general response.
func parseTermGeneralResp(body []byte) map[string]any {
	out := map[string]any{}
	if len(body) >= 5 {
		out["ack_serial"] = int(binary.BigEndian.Uint16(body[0:2]))
		out["ack_msg_id"] = int(binary.BigEndian.Uint16(body[2:4]))
		out["result"] = int(body[4])
	}
	return out
}

// parseTermAttrs decodes a 0x0107 terminal-attributes response (2013 Table 20).
// Best-effort: returns what fits, plus raw_hex.
func parseTermAttrs(body []byte) map[string]any {
	out := map[string]any{"raw_hex": hex.EncodeToString(body)}
	if len(body) < 47 {
		return out
	}
	out["terminal_type"] = int(binary.BigEndian.Uint16(body[0:2]))
	out["manufacturer_id"] = strings.TrimRight(string(body[2:7]), "\x00 ")
	out["terminal_model"] = strings.TrimRight(string(body[7:27]), "\x00 ")
	out["terminal_id"] = strings.TrimRight(string(body[27:34]), "\x00 ")
	out["iccid"] = bcdDigits(body[34:44])
	hwLen := int(body[44])
	off := 45
	if off+hwLen <= len(body) {
		out["hardware_version"] = strings.TrimRight(string(body[off:off+hwLen]), "\x00 ")
		off += hwLen
	}
	if off < len(body) {
		fwLen := int(body[off])
		off++
		if off+fwLen <= len(body) {
			out["firmware_version"] = strings.TrimRight(string(body[off:off+fwLen]), "\x00 ")
		}
	}
	return out
}

// parseVehicleInfo decodes a 0x4040 vehicle-info response: reply serial, a param
// count, then [id(4) len(1) value(len)] params returned as hex by id.
func parseVehicleInfo(body []byte) map[string]any {
	out := map[string]any{"raw_hex": hex.EncodeToString(body)}
	if len(body) < 3 {
		return out
	}
	params := map[string]any{}
	for i := 3; i+5 <= len(body); {
		id := binary.BigEndian.Uint32(body[i : i+4])
		l := int(body[i+4])
		i += 5
		if i+l > len(body) {
			break
		}
		params[hex.EncodeToString(body[i-5:i-1])] = hex.EncodeToString(body[i : i+l])
		_ = id
		i += l
	}
	out["params"] = params
	return out
}
