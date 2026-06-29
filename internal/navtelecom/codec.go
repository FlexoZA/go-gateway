// Navtelecom NTCB/FLEX wire format (the navtelecom unit's codec).
//
// Reference: docs/navtelecom-integration-plan.md and the vendor spec
// "Navtelecom Communication Protocol v6.2" (dfm-mvr-gateway/docs/Navtelecom).
//
// One TCP connection carries two framings, told apart by the first byte:
//
//	'@' (0x40) NTCB packet  : 16-byte transport header + application body
//	'~' (0x7E) FLEX message : telemetry / FLEX service, no 16-byte header
//	0x7F (DEL) FLEX ping     : 1 byte, no response required
//
// NTCB command bodies (everything starting '*', e.g. *>S, *>FLEX) live inside an
// @NTC frame. FLEX telemetry (~A/~T/~C/~E/~X) is raw and CRC8-checked.
//
// All integers in NTCB and FLEX are little-endian.
package navtelecom

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// Framing markers (first byte of a message on the wire).
const (
	markerNTCB = '@'  // 0x40 — NTCB packet with 16-byte header
	markerFLEX = '~'  // 0x7E — FLEX message
	markerPing = 0x7F // DEL  — FLEX keep-alive ping
)

// ntcbPreamble is the fixed 4-byte NTCB preamble "@NTC".
var ntcbPreamble = []byte{'@', 'N', 'T', 'C'}

const ntcbHeaderLen = 16

// FLEX message type bytes (the char after '~').
const (
	flexArray    = 'A' // 0x41 — array of archived telemetry records
	flexOutOrder = 'T' // 0x54 — single out-of-order/high-priority record
	flexCurrent  = 'C' // 0x43 — current-state record (event id 0xFF00)
	flexArrayExt = 'E' // 0x45 — FLEX 2.0 additional telemetry array
	flexOutExt   = 'X' // 0x58 — FLEX 2.0 additional out-of-order record
)

// eventCurrentState is the reserved event id for a current-state ("ping with
// data") record that carries no real event.
const eventCurrentState = 0xFF00

// FLEX negotiation constants.
const (
	flexProtocol = 0xB0 // <protocol> byte: FLEX
	flexVer10    = 0x0A // protocol/structure version 1.0
)

// capVersion clamps a negotiated FLEX protocol/structure version to 1.0. We only
// decode the stable 1.0 field set (and want no FLEX 2.0+ additional packets), so
// the server reply caps the version and the device downgrades.
func capVersion(v byte) byte {
	if v > flexVer10 {
		return flexVer10
	}
	return v
}

// parseFlexNegotiation parses a `*>FLEX<proto><proto_ver><struct_ver><data_size>
// <bitfield>` body into the negotiated versions and field mask. The bitfield
// length is taken from the body itself (the surrounding NTCB header delimits it),
// so no version-specific length formula is needed.
func parseFlexNegotiation(body []byte) (protoVer, structVer byte, m flexMask, err error) {
	const prefix = "*>FLEX"
	if len(body) < len(prefix)+4 || string(body[:len(prefix)]) != prefix {
		return 0, 0, flexMask{}, fmt.Errorf("navtelecom: not a *>FLEX body")
	}
	// body: [*>FLEX][proto][proto_ver][struct_ver][data_size][bitfield...]
	protoVer = body[7]
	structVer = body[8]
	dataSize := int(body[9])
	bits := body[10:]
	m, err = parseFlexMask(dataSize, bits)
	return protoVer, structVer, m, err
}

// xorSum folds buf with XOR — the NTCB header/body checksum (spec §1.1).
func xorSum(buf []byte) byte {
	var s byte
	for _, b := range buf {
		s ^= b
	}
	return s
}

// crc8 computes the FLEX CRC8 over the whole message including the leading '~'
// (spec Annex B): table-driven, init 0xFF.
func crc8(buf []byte) byte {
	crc := byte(0xFF)
	for _, b := range buf {
		crc = crc8Table[crc^b]
	}
	return crc
}

// crc8Table is the vendor CRC8 lookup table (spec Annex B). Equivalent to the
// bitwise algorithm with polynomial 0x31 (verified in codec_test.go).
var crc8Table = [256]byte{
	0x00, 0x31, 0x62, 0x53, 0xC4, 0xF5, 0xA6, 0x97, 0xB9, 0x88, 0xDB, 0xEA, 0x7D, 0x4C, 0x1F, 0x2E,
	0x43, 0x72, 0x21, 0x10, 0x87, 0xB6, 0xE5, 0xD4, 0xFA, 0xCB, 0x98, 0xA9, 0x3E, 0x0F, 0x5C, 0x6D,
	0x86, 0xB7, 0xE4, 0xD5, 0x42, 0x73, 0x20, 0x11, 0x3F, 0x0E, 0x5D, 0x6C, 0xFB, 0xCA, 0x99, 0xA8,
	0xC5, 0xF4, 0xA7, 0x96, 0x01, 0x30, 0x63, 0x52, 0x7C, 0x4D, 0x1E, 0x2F, 0xB8, 0x89, 0xDA, 0xEB,
	0x3D, 0x0C, 0x5F, 0x6E, 0xF9, 0xC8, 0x9B, 0xAA, 0x84, 0xB5, 0xE6, 0xD7, 0x40, 0x71, 0x22, 0x13,
	0x7E, 0x4F, 0x1C, 0x2D, 0xBA, 0x8B, 0xD8, 0xE9, 0xC7, 0xF6, 0xA5, 0x94, 0x03, 0x32, 0x61, 0x50,
	0xBB, 0x8A, 0xD9, 0xE8, 0x7F, 0x4E, 0x1D, 0x2C, 0x02, 0x33, 0x60, 0x51, 0xC6, 0xF7, 0xA4, 0x95,
	0xF8, 0xC9, 0x9A, 0xAB, 0x3C, 0x0D, 0x5E, 0x6F, 0x41, 0x70, 0x23, 0x12, 0x85, 0xB4, 0xE7, 0xD6,
	0x7A, 0x4B, 0x18, 0x29, 0xBE, 0x8F, 0xDC, 0xED, 0xC3, 0xF2, 0xA1, 0x90, 0x07, 0x36, 0x65, 0x54,
	0x39, 0x08, 0x5B, 0x6A, 0xFD, 0xCC, 0x9F, 0xAE, 0x80, 0xB1, 0xE2, 0xD3, 0x44, 0x75, 0x26, 0x17,
	0xFC, 0xCD, 0x9E, 0xAF, 0x38, 0x09, 0x5A, 0x6B, 0x45, 0x74, 0x27, 0x16, 0x81, 0xB0, 0xE3, 0xD2,
	0xBF, 0x8E, 0xDD, 0xEC, 0x7B, 0x4A, 0x19, 0x28, 0x06, 0x37, 0x64, 0x55, 0xC2, 0xF3, 0xA0, 0x91,
	0x47, 0x76, 0x25, 0x14, 0x83, 0xB2, 0xE1, 0xD0, 0xFE, 0xCF, 0x9C, 0xAD, 0x3A, 0x0B, 0x58, 0x69,
	0x04, 0x35, 0x66, 0x57, 0xC0, 0xF1, 0xA2, 0x93, 0xBD, 0x8C, 0xDF, 0xEE, 0x79, 0x48, 0x1B, 0x2A,
	0xC1, 0xF0, 0xA3, 0x92, 0x05, 0x34, 0x67, 0x56, 0x78, 0x49, 0x1A, 0x2B, 0xBC, 0x8D, 0xDE, 0xEF,
	0x82, 0xB3, 0xE0, 0xD1, 0x46, 0x77, 0x24, 0x15, 0x3B, 0x0A, 0x59, 0x68, 0xFF, 0xCE, 0x9D, 0xAC,
}

// ntcbHeader is a parsed 16-byte NTCB transport header.
type ntcbHeader struct {
	RecipientID uint32
	SenderID    uint32
	BodyLen     int
}

// parseNTCBHeader decodes a 16-byte NTCB header. It validates the preamble and
// the header checksum (CSp); the body checksum (CSd) is validated separately
// once the body has been read.
func parseNTCBHeader(h []byte) (ntcbHeader, error) {
	if len(h) < ntcbHeaderLen {
		return ntcbHeader{}, fmt.Errorf("navtelecom: short NTCB header (%d bytes)", len(h))
	}
	if h[0] != ntcbPreamble[0] || h[1] != ntcbPreamble[1] || h[2] != ntcbPreamble[2] || h[3] != ntcbPreamble[3] {
		return ntcbHeader{}, fmt.Errorf("navtelecom: bad NTCB preamble %x", h[0:4])
	}
	if got, want := h[15], xorSum(h[0:15]); got != want {
		return ntcbHeader{}, fmt.Errorf("navtelecom: NTCB header checksum mismatch got %#x want %#x", got, want)
	}
	return ntcbHeader{
		RecipientID: binary.LittleEndian.Uint32(h[4:8]),
		SenderID:    binary.LittleEndian.Uint32(h[8:12]),
		BodyLen:     int(binary.LittleEndian.Uint16(h[12:14])),
	}, nil
}

// buildNTCB frames an application body in an NTCB packet. recipient/sender are
// the IDs for this direction (the caller swaps them for a reply).
func buildNTCB(recipient, sender uint32, body []byte) []byte {
	out := make([]byte, ntcbHeaderLen+len(body))
	copy(out[0:4], ntcbPreamble)
	binary.LittleEndian.PutUint32(out[4:8], recipient)
	binary.LittleEndian.PutUint32(out[8:12], sender)
	binary.LittleEndian.PutUint16(out[12:14], uint16(len(body)))
	out[14] = xorSum(body)
	out[15] = xorSum(out[0:15])
	copy(out[ntcbHeaderLen:], body)
	return out
}

// flexFieldSize maps a FLEX telemetry field number (1-based, the bit index + 1
// in the negotiation mask) to its fixed size in bytes (spec Annex A.1, FLEX
// 1.0/2.0/3.0 fields 1–142). A record is the present fields concatenated in
// ascending field order with no gaps, so the layout is fully determined by the
// negotiated mask plus this table.
var flexFieldSize = map[int]int{
	1: 4, 2: 2, 3: 4, 4: 1, 5: 1, 6: 1, 7: 1, 8: 1, 9: 4, 10: 4,
	11: 4, 12: 4, 13: 4, 14: 2, 15: 4, 16: 4, 17: 2, 18: 2, 19: 2, 20: 2,
	21: 2, 22: 2, 23: 2, 24: 2, 25: 2, 26: 2, 27: 2, 28: 2, 29: 1, 30: 1,
	31: 1, 32: 1, 33: 4, 34: 4, 35: 2, 36: 2, 37: 4, 38: 2, 39: 2, 40: 2,
	41: 2, 42: 2, 43: 2, 44: 2, 45: 1, 46: 1, 47: 1, 48: 1, 49: 1, 50: 1,
	51: 1, 52: 1, 53: 2, 54: 4, 55: 2, 56: 1, 57: 4, 58: 2, 59: 2, 60: 2,
	61: 2, 62: 2, 63: 1, 64: 1, 65: 1, 66: 2, 67: 4, 68: 2, 69: 1,
	// FLEX 2.0
	70: 8, 71: 2, 72: 1, 73: 16, 74: 4, 75: 2, 76: 4, 77: 37, 78: 1, 79: 1,
	80: 1, 81: 1, 82: 1, 83: 1, 84: 3, 85: 3, 86: 3, 87: 3, 88: 3, 89: 3,
	90: 3, 91: 3, 92: 3, 93: 3, 94: 6, 95: 12, 96: 24, 97: 48, 98: 1, 99: 1,
	100: 2, 101: 1, 102: 4, 103: 4, 104: 1, 105: 4, 106: 2, 107: 6, 108: 2, 109: 6,
	110: 2, 111: 2, 112: 2, 113: 2, 114: 2, 115: 2, 116: 2, 117: 2, 118: 1, 119: 2,
	120: 2, 121: 2, 122: 1,
	// FLEX 3.0
	123: 1, 124: 1, 125: 1, 126: 1, 127: 4, 128: 4, 129: 4, 130: 4, 131: 4, 132: 4,
	133: 2, 134: 2, 135: 2, 136: 2, 137: 2, 138: 2, 139: 1, 140: 1, 141: 2, 142: 3,
}

// flexMask is the negotiated set of present telemetry fields: the ordered field
// numbers and the resulting fixed record length in bytes.
type flexMask struct {
	fields    []int // ascending FLEX field numbers present in each record
	recordLen int   // bytes per telemetry record
}

// parseFlexMask turns the negotiation bitfield into an ordered field list and a
// record length. dataSize is the negotiated field count; bits is the raw mask
// (LSB of byte 0 = field 1). An error is returned if a set bit names a field
// whose size this build doesn't know — we'd be unable to frame records, so the
// session refuses the connection rather than mis-decode.
func parseFlexMask(dataSize int, bits []byte) (flexMask, error) {
	var m flexMask
	for field := 1; field <= dataSize; field++ {
		byteIdx := (field - 1) / 8
		bitIdx := (field - 1) % 8
		if byteIdx >= len(bits) {
			break
		}
		if bits[byteIdx]&(1<<bitIdx) == 0 {
			continue
		}
		size, ok := flexFieldSize[field]
		if !ok {
			return flexMask{}, fmt.Errorf("navtelecom: FLEX mask sets unsupported field %d", field)
		}
		m.fields = append(m.fields, field)
		m.recordLen += size
	}
	if m.recordLen == 0 {
		return flexMask{}, fmt.Errorf("navtelecom: empty FLEX mask")
	}
	sort.Ints(m.fields) // fields are added in order already; guard anyway
	return m, nil
}

// flexRecord is the decoded subset of a telemetry record we forward. Presence
// flags distinguish "field absent from mask" from "field present with value 0".
type flexRecord struct {
	RecordNum  uint32 // field 1
	EventID    uint16 // field 2
	HasEventID bool
	EventTime  uint32 // field 3 — epoch seconds
	HasTime    bool

	GPSStatus    uint8 // field 8: bit1 valid nav, bits2-7 satellite count
	HasGPSStatus bool
	ValidTime    uint32 // field 9 — time of last valid coordinates
	HasValidTime bool

	Lat    int32 // field 10 — ten-thousandths of a minute
	Lon    int32 // field 11
	HasLat bool
	HasLon bool
	Height int32 // field 12 — decimeters
	HasAlt bool

	Speed     float32 // field 13 — km/h
	HasSpeed  bool
	Course    uint16 // field 14 — degrees
	HasCourse bool
	Mileage   float32 // field 15 — km
	HasMile   bool

	GSMLevel    uint8 // field 7 — 0..31, 99 = no signal
	HasGSMLevel bool

	MainVoltage    uint16 // field 19 — mV
	HasMainVoltage bool
	BackupVoltage  uint16 // field 20 — mV
	HasBackupV     bool

	Ain        [8]uint16 // fields 21..28 — mV
	AinPresent [8]bool

	Discrete1    uint8 // field 29 — inputs In1..In8
	HasDiscrete1 bool
	Discrete2    uint8 // field 30 — inputs In9..In16
	HasDiscrete2 bool
	Outputs1     uint8 // field 31 — outputs Out1..Out8
	HasOutputs1  bool
	Outputs2     uint8 // field 32 — outputs Out9..Out16
	HasOutputs2  bool
}

// decodeRecord decodes one telemetry record of m.recordLen bytes. It reads each
// present field by its fixed width, decoding the fields we forward and skipping
// the rest. data must be exactly m.recordLen bytes (the caller slices it).
func decodeRecord(m flexMask, data []byte) (flexRecord, error) {
	if len(data) != m.recordLen {
		return flexRecord{}, fmt.Errorf("navtelecom: record length %d, want %d", len(data), m.recordLen)
	}
	var r flexRecord
	off := 0
	for _, field := range m.fields {
		size := flexFieldSize[field]
		b := data[off : off+size]
		switch field {
		case 1:
			r.RecordNum = binary.LittleEndian.Uint32(b)
		case 2:
			r.EventID = binary.LittleEndian.Uint16(b)
			r.HasEventID = true
		case 3:
			r.EventTime = binary.LittleEndian.Uint32(b)
			r.HasTime = true
		case 7:
			r.GSMLevel = b[0]
			r.HasGSMLevel = true
		case 8:
			r.GPSStatus = b[0]
			r.HasGPSStatus = true
		case 9:
			r.ValidTime = binary.LittleEndian.Uint32(b)
			r.HasValidTime = true
		case 10:
			r.Lat = int32(binary.LittleEndian.Uint32(b))
			r.HasLat = true
		case 11:
			r.Lon = int32(binary.LittleEndian.Uint32(b))
			r.HasLon = true
		case 12:
			r.Height = int32(binary.LittleEndian.Uint32(b))
			r.HasAlt = true
		case 13:
			r.Speed = math.Float32frombits(binary.LittleEndian.Uint32(b))
			r.HasSpeed = true
		case 14:
			r.Course = binary.LittleEndian.Uint16(b)
			r.HasCourse = true
		case 15:
			r.Mileage = math.Float32frombits(binary.LittleEndian.Uint32(b))
			r.HasMile = true
		case 19:
			r.MainVoltage = binary.LittleEndian.Uint16(b)
			r.HasMainVoltage = true
		case 20:
			r.BackupVoltage = binary.LittleEndian.Uint16(b)
			r.HasBackupV = true
		case 21, 22, 23, 24, 25, 26, 27, 28:
			idx := field - 21
			r.Ain[idx] = binary.LittleEndian.Uint16(b)
			r.AinPresent[idx] = true
		case 29:
			r.Discrete1 = b[0]
			r.HasDiscrete1 = true
		case 30:
			r.Discrete2 = b[0]
			r.HasDiscrete2 = true
		case 31:
			r.Outputs1 = b[0]
			r.HasOutputs1 = true
		case 32:
			r.Outputs2 = b[0]
			r.HasOutputs2 = true
		default:
			// Field present in the mask but not forwarded — skip by width.
		}
		off += size
	}
	return r, nil
}

// latLonDegrees converts a FLEX coordinate (signed ten-thousandths of a minute)
// to decimal degrees: value / 10000 minutes / 60. e.g. 33422389 → 55.70398°.
func latLonDegrees(v int32) float64 {
	return float64(v) / 600000.0
}

// utc picks the record's timestamp, preferring the event time (field 3) and
// falling back to the time of last valid coordinates (field 9).
func (r flexRecord) utc() (int64, bool) {
	if r.HasTime && r.EventTime != 0 {
		return int64(r.EventTime), true
	}
	if r.HasValidTime && r.ValidTime != 0 {
		return int64(r.ValidTime), true
	}
	return 0, false
}

// positioning reports whether the record carries a valid navigation fix (field 8
// bit 1). Absent the GPS-status field we fall back to "have coordinates".
func (r flexRecord) positioning() bool {
	if r.HasGPSStatus {
		return r.GPSStatus&0x02 != 0
	}
	return r.HasLat && r.HasLon
}

// satellites returns the satellite count from field 8 (bits 2..7), if present.
func (r flexRecord) satellites() (int, bool) {
	if !r.HasGPSStatus {
		return 0, false
	}
	return int(r.GPSStatus>>2) & 0x3F, true
}

// buildPayload assembles the normalized field map the universal message builder
// understands (see internal/core/message/message.go). serial is the device IMEI.
func buildPayload(serial string, r flexRecord) map[string]any {
	payload := map[string]any{"imei": serial}
	if ts, ok := r.utc(); ok {
		payload["utc"] = float64(ts)
	}
	if r.HasLat {
		payload["latitude"] = latLonDegrees(r.Lat)
	}
	if r.HasLon {
		payload["longitude"] = latLonDegrees(r.Lon)
	}
	if r.HasLat || r.HasLon {
		if r.positioning() {
			payload["positioning"] = float64(1)
		} else {
			payload["positioning"] = float64(0)
		}
	}
	if r.HasSpeed {
		payload["speed"] = float64(r.Speed)
	}
	if r.HasCourse {
		payload["bearing"] = float64(r.Course)
	}
	if sats, ok := r.satellites(); ok {
		payload["satellites"] = float64(sats)
	}
	if r.HasAlt {
		payload["altitude"] = float64(r.Height) / 10.0 // decimeters → meters
	}
	if r.HasMile {
		payload["mileage_km"] = float64(r.Mileage)
	}
	if r.HasGSMLevel && r.GSMLevel != 99 {
		payload["signal"] = float64(r.GSMLevel)
	}

	// Analog inputs (mV) in fixed Ain1..Ain8 order; only present channels emitted.
	var an []any
	for i := 0; i < 8; i++ {
		if r.AinPresent[i] {
			an = append(an, float64(r.Ain[i]))
		}
	}
	if r.HasMainVoltage {
		an = append([]any{float64(r.MainVoltage)}, an...)
	}
	if len(an) > 0 {
		payload["an_inputs"] = an
	}

	// Discrete inputs / outputs as bit arrays (In1.. / Out1..).
	if in := bitArray(r.Discrete1, r.HasDiscrete1, r.Discrete2, r.HasDiscrete2); len(in) > 0 {
		payload["inputs"] = in
	}
	if out := bitArray(r.Outputs1, r.HasOutputs1, r.Outputs2, r.HasOutputs2); len(out) > 0 {
		payload["outputs"] = out
	}
	return payload
}

// bitArray expands up to two present status bytes into an array of 0/1 values,
// low bit first. Absent bytes contribute nothing.
func bitArray(lo uint8, hasLo bool, hi uint8, hasHi bool) []any {
	var out []any
	appendByte := func(v uint8) {
		for i := 0; i < 8; i++ {
			out = append(out, float64((v>>i)&1))
		}
	}
	if hasLo {
		appendByte(lo)
	}
	if hasHi {
		appendByte(hi)
	}
	return out
}
