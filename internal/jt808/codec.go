package jt808

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// JT/T 808 (2019) wire format for the N62 dashcam, ported from the old Node
// gateway (dfm-mvr-gateway/src/tcp/jt808Server.js). Frames are delimited by a
// 0x7E flag and escaped; the body is a big-endian header + message body, with a
// trailing 1-byte XOR checksum.
//
// See docs/jt808-integration-plan.md for the full layout.

const (
	flag    = 0x7e // frame start/end delimiter
	escByte = 0x7d // escape introducer
	escFlag = 0x02 // 0x7d 0x02 -> 0x7e
	escEsc  = 0x01 // 0x7d 0x01 -> 0x7d

	// maxFrameBytes guards an unescaped control frame. Control messages are small;
	// the big ULV video frames arrive on the separate media port, not here.
	maxFrameBytes = 64 * 1024

	// maxReasmBytes caps the aggregate size of a subpackaged message held mid-
	// reassembly. Fragments are device-controlled: a peer can advertise SubTotal
	// up to 0xFFFF and dribble ~64 KiB per fragment, so without an aggregate cap
	// a single unauthenticated session can buffer gigabytes. 8 MiB comfortably
	// covers real config replies and snapshot JPEGs while bounding the exposure.
	maxReasmBytes = 8 * 1024 * 1024
)

// Inbound (terminal -> platform) message IDs.
const (
	msgRegister           = 0x0100
	msgAuth               = 0x0102
	msgHeartbeat          = 0x0002
	msgLocation           = 0x0200
	msgBatchLocation      = 0x0704
	msgTermGeneralResp    = 0x0001
	msgTermAttrs          = 0x0107
	msgUlvParamResp       = 0xb051
	msgVehicleInfo        = 0x4040
	msgTransparentUp      = 0x0900
	msgDriverCard         = 0x0702
	msgMultimediaEvent    = 0x0800
	msgMultimediaData     = 0x0801
	msgCameraShootResp    = 0x0805
	msgPassengerCount     = 0x0d03
	msgFileUploadComplete = 0x1206
	msgResourceList       = 0x1205
)

// Outbound (platform -> terminal) message IDs.
const (
	msgPlatformGeneralResp = 0x8001
	msgRegisterResp        = 0x8100
	msgTerminalControl     = 0x8105
	msgQueryTermAttrs      = 0x8107
	msgVehicleInfoQuery    = 0xb040
	msgUlvParam            = 0xb050
	msgTransparentDown     = 0x8900

	// JT1078 video control plane.
	msgRealtimeAV      = 0x9101 // start live (real-time A/V request)
	msgStopAV          = 0x9102 // stop live
	msgPlaybackAV      = 0x9201 // playback request (recorded clip)
	msgPlaybackControl = 0x9202 // playback control (stop)
	msgResourceQuery   = 0x9205 // recording resource list query
	msgCameraShoot     = 0x8801 // camera immediate shooting command
)

// General-response result codes (0x8001 byte 4 / 0x8100 byte 2).
const (
	resultOK = 0x00
)

// unescape reverses the 0x7d escaping. Returns the decoded bytes, or an error on
// a malformed escape sequence.
func unescape(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		b := in[i]
		if b != escByte {
			out = append(out, b)
			continue
		}
		if i+1 >= len(in) {
			return nil, fmt.Errorf("jt808: dangling escape")
		}
		switch in[i+1] {
		case escFlag:
			out = append(out, flag)
		case escEsc:
			out = append(out, escByte)
		default:
			return nil, fmt.Errorf("jt808: bad escape 0x7d 0x%02x", in[i+1])
		}
		i++
	}
	return out, nil
}

// escape applies the 0x7d escaping to a raw frame body (header+body+checksum).
func escape(in []byte) []byte {
	out := make([]byte, 0, len(in)+8)
	for _, b := range in {
		switch b {
		case flag:
			out = append(out, escByte, escFlag)
		case escByte:
			out = append(out, escByte, escEsc)
		default:
			out = append(out, b)
		}
	}
	return out
}

// xorChecksum is the BCC over all bytes (header through last body byte).
func xorChecksum(b []byte) byte {
	var c byte
	for _, x := range b {
		c ^= x
	}
	return c
}

// header is a decoded JT808 message header.
type header struct {
	MsgID      uint16
	BodyLen    int
	Subpackage bool
	Is2019     bool
	Phone      string // decoded terminal-phone digits (leading zeros intact)
	Serial     uint16 // terminal message serial
	HeaderLen  int    // bytes consumed by the header (incl. subpackage info)
	SubTotal   uint16 // subpackage: total packet count (0 when not subpackaged)
	SubIndex   uint16 // subpackage: 1-based packet index
}

// parseHeader decodes the header from an unescaped, checksum-validated frame
// (header+body, no flags, no checksum). It auto-detects 2013 vs 2019 by body-attr
// bit 14. body is the slice after the header, bounded by the declared body length.
func parseHeader(buf []byte) (header, []byte, error) {
	if len(buf) < 4 {
		return header{}, nil, fmt.Errorf("jt808: short frame (%d bytes)", len(buf))
	}
	h := header{
		MsgID: binary.BigEndian.Uint16(buf[0:2]),
	}
	attrs := binary.BigEndian.Uint16(buf[2:4])
	h.BodyLen = int(attrs & 0x03ff)
	h.Subpackage = attrs&(1<<13) != 0
	h.Is2019 = attrs&(1<<14) != 0

	if h.Is2019 {
		// id(2) attrs(2) version(1) phone(10) serial(2) [subpkg(4)]
		if len(buf) < 17 {
			return header{}, nil, fmt.Errorf("jt808: short 2019 header (%d bytes)", len(buf))
		}
		h.Phone = bcdDigits(buf[5:15])
		h.Serial = binary.BigEndian.Uint16(buf[15:17])
		h.HeaderLen = 17
	} else {
		// id(2) attrs(2) phone(6) serial(2) [subpkg(4)]
		if len(buf) < 12 {
			return header{}, nil, fmt.Errorf("jt808: short 2013 header (%d bytes)", len(buf))
		}
		h.Phone = bcdDigits(buf[4:10])
		h.Serial = binary.BigEndian.Uint16(buf[10:12])
		h.HeaderLen = 12
	}
	if h.Subpackage {
		// Subpackage info: total-packets(2) + 1-based packet-index(2).
		if h.HeaderLen+4 <= len(buf) {
			h.SubTotal = binary.BigEndian.Uint16(buf[h.HeaderLen : h.HeaderLen+2])
			h.SubIndex = binary.BigEndian.Uint16(buf[h.HeaderLen+2 : h.HeaderLen+4])
		}
		h.HeaderLen += 4
	}
	if h.HeaderLen > len(buf) {
		return header{}, nil, fmt.Errorf("jt808: header overruns frame")
	}
	body := buf[h.HeaderLen:]
	// Trust the declared body length when it fits; some firmware pads the frame.
	if h.BodyLen >= 0 && h.BodyLen <= len(body) {
		body = body[:h.BodyLen]
	}
	return h, body, nil
}

// bcdDigits decodes a BCD byte slice to a digit string. Non-decimal nibbles are
// coerced to 0 (mirrors the JS, robust to padding like 0xF).
func bcdDigits(b []byte) string {
	var sb strings.Builder
	for _, x := range b {
		hi, lo := x>>4, x&0x0f
		if hi > 9 {
			hi = 0
		}
		if lo > 9 {
			lo = 0
		}
		sb.WriteByte('0' + hi)
		sb.WriteByte('0' + lo)
	}
	return sb.String()
}

// serialFromPhone turns decoded phone digits into the gateway serial suffix:
// leading zeros stripped (the JS behaviour), "0" when all-zero.
func serialFromPhone(digits string) string {
	d := strings.TrimLeft(digits, "0")
	if d == "" {
		d = "0"
	}
	return "JT808_" + d
}

// buildFrame frames a message: header(2019) + body, XOR checksum, escaping, and
// the surrounding 0x7e flags. phone is the terminal-phone digit string; serial is
// the platform message serial.
func buildFrame(msgID uint16, phone string, serial uint16, body []byte) []byte {
	raw := make([]byte, 0, 17+len(body)+1)
	var idb [2]byte
	binary.BigEndian.PutUint16(idb[:], msgID)
	raw = append(raw, idb[:]...)

	attrs := uint16(len(body) & 0x03ff)
	attrs |= 1 << 14 // 2019 format
	var ab [2]byte
	binary.BigEndian.PutUint16(ab[:], attrs)
	raw = append(raw, ab[:]...)

	raw = append(raw, 0x01)               // protocol version
	raw = append(raw, phoneBCD(phone)...) // BCD[10]
	var sb [2]byte
	binary.BigEndian.PutUint16(sb[:], serial)
	raw = append(raw, sb[:]...)

	raw = append(raw, body...)
	raw = append(raw, xorChecksum(raw))

	out := make([]byte, 0, len(raw)+8)
	out = append(out, flag)
	out = append(out, escape(raw)...)
	out = append(out, flag)
	return out
}

// phoneBCD encodes a phone digit string as BCD[10] (20 digits, left-padded with
// zeros), matching the 2019 header field.
func phoneBCD(digits string) []byte {
	const width = 20
	if len(digits) > width {
		digits = digits[len(digits)-width:]
	}
	digits = strings.Repeat("0", width-len(digits)) + digits
	out := make([]byte, 10)
	for i := 0; i < 10; i++ {
		hi := digits[2*i] - '0'
		lo := digits[2*i+1] - '0'
		out[i] = hi<<4 | lo
	}
	return out
}

// buildGeneralResp builds a 0x8001 platform general response acking a terminal
// message (ack-serial, ack-id, result).
func buildGeneralResp(phone string, platSerial, ackSerial, ackID uint16, result byte) []byte {
	body := make([]byte, 5)
	binary.BigEndian.PutUint16(body[0:2], ackSerial)
	binary.BigEndian.PutUint16(body[2:4], ackID)
	body[4] = result
	return buildFrame(msgPlatformGeneralResp, phone, platSerial, body)
}

// buildRegisterResp builds a 0x8100 registration response: ack-serial, result,
// and (on success) the auth code string.
func buildRegisterResp(phone string, platSerial, ackSerial uint16, result byte, auth string) []byte {
	body := make([]byte, 3, 3+len(auth))
	binary.BigEndian.PutUint16(body[0:2], ackSerial)
	body[2] = result
	if result == resultOK {
		body = append(body, []byte(auth)...)
	}
	return buildFrame(msgRegisterResp, phone, platSerial, body)
}

// authCode is the stable auth string we issue at registration: "DFM" + the last
// six phone digits (mirrors the JS).
func authCode(digits string) string {
	last := digits
	if len(last) > 6 {
		last = last[len(last)-6:]
	}
	return "DFM" + last
}

// location is a decoded 0x0200 location report.
type location struct {
	Alarm     uint32
	Status    uint32
	Latitude  float64
	Longitude float64
	Altitude  int
	Speed     float64 // km/h
	Direction int
	TimeUTC   int64
	TLVs      map[byte][]byte
}

// parseLocation decodes a 0x0200 body: 28 fixed bytes then additional-info TLVs.
// offsetHours converts the device-local BCD time to UTC. Returns false if the
// fixed part is too short.
func parseLocation(body []byte, offsetHours float64) (location, bool) {
	if len(body) < 28 {
		return location{}, false
	}
	loc := location{
		Alarm:     binary.BigEndian.Uint32(body[0:4]),
		Status:    binary.BigEndian.Uint32(body[4:8]),
		Altitude:  int(int16(binary.BigEndian.Uint16(body[16:18]))),
		Speed:     float64(binary.BigEndian.Uint16(body[18:20])) / 10.0,
		Direction: int(binary.BigEndian.Uint16(body[20:22])),
		TLVs:      map[byte][]byte{},
	}
	lat := float64(binary.BigEndian.Uint32(body[8:12])) / 1e6
	lon := float64(binary.BigEndian.Uint32(body[12:16])) / 1e6
	if loc.Status&(1<<2) != 0 { // bit 2: south latitude
		lat = -lat
	}
	if loc.Status&(1<<3) != 0 { // bit 3: west longitude
		lon = -lon
	}
	loc.Latitude = lat
	loc.Longitude = lon
	loc.TimeUTC = parseBCDTime(body[22:28], offsetHours)

	// Additional-info TLVs: id(1) len(1) value(len).
	for i := 28; i+2 <= len(body); {
		id := body[i]
		l := int(body[i+1])
		i += 2
		if i+l > len(body) {
			break
		}
		loc.TLVs[id] = append([]byte(nil), body[i:i+l]...)
		i += l
	}
	return loc, true
}

// parseBCDTime decodes a BCD[6] YYMMDDHHMMSS in the device's local zone to a unix
// timestamp, subtracting offsetHours to reach UTC. Returns 0 on a malformed time.
func parseBCDTime(b []byte, offsetHours float64) int64 {
	if len(b) < 6 {
		return 0
	}
	d := bcdDigits(b) // 12 digits
	yy, _ := strconv.Atoi(d[0:2])
	mo, _ := strconv.Atoi(d[2:4])
	dd, _ := strconv.Atoi(d[4:6])
	hh, _ := strconv.Atoi(d[6:8])
	mi, _ := strconv.Atoi(d[8:10])
	ss, _ := strconv.Atoi(d[10:12])
	if mo < 1 || mo > 12 || dd < 1 || dd > 31 {
		return 0
	}
	t := time.Date(2000+yy, time.Month(mo), dd, hh, mi, ss, 0, time.UTC)
	return t.Unix() - int64(offsetHours*3600)
}

// bcdTimeFromUTC encodes a unix UTC time as BCD[6] YYMMDDHHMMSS in the device's
// local zone (UTC + offsetHours) — the inverse of parseBCDTime, used for the
// start/end windows in playback (0x9201) and recording-query (0x9205) requests.
func bcdTimeFromUTC(unix int64, offsetHours float64) []byte {
	t := time.Unix(unix, 0).UTC().Add(time.Duration(offsetHours * float64(time.Hour)))
	enc := func(n int) byte { return byte((n/10)<<4 | n%10) }
	return []byte{
		enc(t.Year() % 100), enc(int(t.Month())), enc(t.Day()),
		enc(t.Hour()), enc(t.Minute()), enc(t.Second()),
	}
}

// buildCameraShoot builds a 0x8801 camera-immediate-shooting body: capture
// `count` still(s) on `channel` and upload them to the platform in realtime.
// resolution 0 = use the device's configured resolution; quality is 1 (best)..10.
func buildCameraShoot(channel, count, resolution, quality int) []byte {
	body := make([]byte, 12)
	body[0] = byte(channel)
	binary.BigEndian.PutUint16(body[1:3], uint16(count)) // shooting command: n photos
	binary.BigEndian.PutUint16(body[3:5], 0)             // interval (s)
	body[5] = 0                                          // save flag: 0 = realtime upload
	body[6] = byte(resolution)                           // resolution (0 = per config)
	body[7] = byte(quality)                              // quality 1..10
	// bytes 8..11 brightness/contrast/saturation/chroma left at 0 (device default).
	return body
}

// encodeIP returns a 1-byte length prefix followed by the ASCII host/IP, as the
// server-address field of the video commands expects.
func encodeIP(host string) []byte {
	b := []byte(host)
	if len(b) > 255 {
		b = b[:255]
	}
	return append([]byte{byte(len(b))}, b...)
}

// splitBatch splits a 0x0704 batch-location body into its inner 0x0200 bodies.
// Layout: count(2) type(1) then [len(2) body(len)] items.
func splitBatch(body []byte) [][]byte {
	if len(body) < 3 {
		return nil
	}
	var out [][]byte
	for i := 3; i+2 <= len(body); {
		l := int(binary.BigEndian.Uint16(body[i : i+2]))
		i += 2
		if l <= 0 || i+l > len(body) {
			break
		}
		out = append(out, body[i:i+l])
		i += l
	}
	return out
}
