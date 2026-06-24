// Concox GT06 GPS tracker protocol codec (the FleeTiger unit's wire format).
//
// Reference: docs/fleetiger/GT06/GT06_GPS_Tracker_Communication_Protocol_v1.8.1.pdf
// Ported from the original JS gateway's src/tcp/gt06Codec.js.
//
// Frame layout (single-byte length variant, start bits 0x78 0x78):
//
//	[0..1]   start bits        0x78 0x78
//	[2]      length byte       = protocol(1) + content(N) + serial(2) + crc(2) = N + 5
//	[3]      protocol number   0x01 login | 0x12 location | 0x13 status | 0x16 alarm | ...
//	[4..]    information content (N bytes)
//	[.. ]    information serial number (2 bytes, big-endian)
//	[.. ]    error check (CRC-ITU, 2 bytes, big-endian)
//	[last-1] stop bits         0x0D 0x0A
//
// The CRC-ITU (a.k.a. CRC-16/X25) covers the length byte through the serial
// number inclusive (spec §4.6).
package fleetiger

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

// GT06 protocol (message type) numbers.
const (
	protoLogin    = 0x01 // device → server: IMEI registration
	protoLocation = 0x12 // device → server: GPS + LBS (no ACK)
	protoStatus   = 0x13 // device → server: heartbeat / status (ACK)
	protoString   = 0x15 // device → server: command echo
	protoAlarm    = 0x16 // device → server: GPS + LBS + status + alarm (ACK)
	protoGPSAddr  = 0x1a // device → server: GPS + phone number
	protoServer   = 0x80 // server → device: remote command
)

// protocolNeedsAck reports whether the server must answer a packet with a 5-byte
// response so the device keeps the connection alive. Login and heartbeat are
// mandatory; alarms should be acknowledged. Location (0x12) needs no response.
func protocolNeedsAck(protocol int) bool {
	switch protocol {
	case protoLogin, protoStatus, protoAlarm:
		return true
	default:
		return false
	}
}

// crc16Itu computes CRC-ITU / CRC-16/X25 over buf[start:end]: poly 0x1021
// reflected (0x8408), init 0xFFFF, xorout 0xFFFF.
func crc16Itu(buf []byte, start, end int) uint16 {
	fcs := uint16(0xffff)
	for i := start; i < end; i++ {
		fcs ^= uint16(buf[i])
		for bit := 0; bit < 8; bit++ {
			if fcs&0x0001 != 0 {
				fcs = (fcs >> 1) ^ 0x8408
			} else {
				fcs >>= 1
			}
		}
	}
	return ^fcs
}

// decodeBcdImei decodes the 8-byte Terminal ID, which carries a 15-digit IMEI in
// BCD with a leading 0 nibble (spec §5.1.1.4). e.g.
// 0x01 0x23 0x45 0x67 0x89 0x01 0x23 0x45 -> "123456789012345".
func decodeBcdImei(buf []byte) string {
	digits := hex.EncodeToString(buf)
	trimmed := strings.TrimLeft(digits, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

// decodeCoordinate converts an encoded coordinate to decimal degrees. The wire
// value is (degrees*60 + minutes) * 30000 (spec §5.2.1.6).
func decodeCoordinate(raw uint32, negative bool) float64 {
	degrees := float64(raw) / 30000 / 60
	if negative {
		return -degrees
	}
	return degrees
}

// decodeDateTime decodes the 6-byte YY(−2000)/MM/DD/HH/MM/SS field at off to unix
// seconds. The device sends wall-clock in its local timezone; tzOffsetHours shifts
// it back to UTC. Returns 0 on an out-of-range value.
func decodeDateTime(content []byte, off int, tzOffsetHours float64) int64 {
	year := 2000 + int(content[off])
	month := int(content[off+1])
	day := int(content[off+2])
	hour := int(content[off+3])
	minute := int(content[off+4])
	second := int(content[off+5])
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return 0
	}
	wallClock := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC).Unix()
	return wallClock - int64(math.Round(tzOffsetHours*3600))
}

// courseStatus is the decoded 2-byte course/status field (spec §5.2.1.8).
type courseStatus struct {
	Positioning int  // bit4: 1 = GPS has been positioned
	LatNorth    bool // bit2: 1 = North, 0 = South
	LonWest     bool // bit3: 1 = West, 0 = East
	Course      int  // 10-bit heading 0..360
}

func decodeCourseStatus(b1, b2 byte) courseStatus {
	cs := courseStatus{
		LatNorth: b1&0x04 != 0,
		LonWest:  b1&0x08 != 0,
		Course:   (int(b1&0x03) << 8) | int(b2),
	}
	if b1&0x10 != 0 {
		cs.Positioning = 1
	}
	return cs
}

// lbsInfo is a decoded cell-tower (LBS) location block.
type lbsInfo struct {
	MCC    int
	MNC    int
	LAC    int
	CellID int
}

// decodeLbs decodes the LBS block at off, or nil when the content is too short.
func decodeLbs(content []byte, off int) *lbsInfo {
	if len(content) < off+8 {
		return nil
	}
	return &lbsInfo{
		MCC:    int(binary.BigEndian.Uint16(content[off : off+2])),
		MNC:    int(content[off+2]),
		LAC:    int(binary.BigEndian.Uint16(content[off+3 : off+5])),
		CellID: int(content[off+5])<<16 | int(content[off+6])<<8 | int(content[off+7]),
	}
}

// gpsBlock is the decoded GPS information shared by location (0x12) and alarm
// (0x16) packets.
type gpsBlock struct {
	UTC         int64
	Satellites  int
	Latitude    float64
	Longitude   float64
	Speed       int
	Bearing     int
	Positioning int
	LBS         *lbsInfo
	HasIgnition bool // set only for alarm packets (carry a status block)
	Ignition    int
}

// decodeGpsBlock decodes the 18-byte GPS block at the start of content. The
// caller must ensure len(content) >= 18.
func decodeGpsBlock(content []byte, tzOffsetHours float64) gpsBlock {
	cs := decodeCourseStatus(content[16], content[17])
	return gpsBlock{
		UTC:         decodeDateTime(content, 0, tzOffsetHours),
		Satellites:  int(content[6] & 0x0f),
		Latitude:    decodeCoordinate(binary.BigEndian.Uint32(content[7:11]), !cs.LatNorth),
		Longitude:   decodeCoordinate(binary.BigEndian.Uint32(content[11:15]), cs.LonWest),
		Speed:       int(content[15]),
		Bearing:     cs.Course,
		Positioning: cs.Positioning,
	}
}

// statusInfo is the decoded terminal-status block (heartbeat 0x13 and the tail of
// alarm 0x16).
type statusInfo struct {
	TerminalInfo int
	VoltageLevel int
	GSMSignal    int
	AlarmFormer  int
	Language     int
	Ignition     int // bit1: ACC high
	Charging     int // bit2: charge on
	OilCut       int // bit7: oil/electricity disconnected
}

// decodeStatusInfo decodes the 5-byte status block at off, or nil when too short.
func decodeStatusInfo(content []byte, off int) *statusInfo {
	if len(content) < off+5 {
		return nil
	}
	terminalInfo := int(content[off])
	si := &statusInfo{
		TerminalInfo: terminalInfo,
		VoltageLevel: int(content[off+1]),
		GSMSignal:    int(content[off+2]),
		AlarmFormer:  int(content[off+3]),
		Language:     int(content[off+4]),
	}
	if terminalInfo&0x02 != 0 {
		si.Ignition = 1
	}
	if terminalInfo&0x04 != 0 {
		si.Charging = 1
	}
	if terminalInfo&0x80 != 0 {
		si.OilCut = 1
	}
	return si
}

// alarmEventCodes maps the alarm/language former byte to an ACM-style event code.
// These are provisional and should be confirmed against the ACM Standard Event
// Code table once a real device alarm sample is available.
var alarmEventCodes = map[int]string{
	0x01: "PANIC",           // SOS
	0x02: "POWER:CUT",       // power cut
	0x03: "ALARM:VIBRATION", // shock
	0x04: "GEOFENCE:ENTER",  // fence in
	0x05: "GEOFENCE:EXIT",   // fence out
}

// eventsFromAlarm derives event codes from an alarm packet's former byte and
// status block.
func eventsFromAlarm(alarmFormer int, si *statusInfo) []string {
	var events []string
	if mapped, ok := alarmEventCodes[alarmFormer]; ok {
		events = append(events, mapped)
	}
	// Low battery is signalled via terminal-info alarm bits (011) rather than the
	// alarm/language former byte.
	if si != nil && (si.TerminalInfo>>3)&0x07 == 0b011 {
		events = append(events, "BATTERY:LOW")
	}
	return events
}

// parsedPacket is a decoded GT06 packet (start bits through stop bits inclusive).
type parsedPacket struct {
	Protocol   int
	Content    []byte
	SerialNo   uint16
	CRCValid   bool
	IMEI       string      // login (0x01) only
	GPS        *gpsBlock   // location (0x12) / alarm (0x16)
	StatusInfo *statusInfo // status (0x13) / alarm (0x16)
	Events     []string    // alarm (0x16)
}

// parseGt06Packet parses a single framed GT06 packet. It returns an error only on
// a structural framing problem; a bad CRC is reported via parsedPacket.CRCValid so
// the caller can drop the packet without dropping the connection.
func parseGt06Packet(packet []byte, tzOffsetHours float64) (*parsedPacket, error) {
	if len(packet) < 10 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(packet))
	}
	if packet[0] != 0x78 || packet[1] != 0x78 {
		return nil, fmt.Errorf("missing 0x78 0x78 start bits")
	}
	total := int(packet[2]) + 5
	if len(packet) != total {
		return nil, fmt.Errorf("length mismatch: header says %d bytes, got %d", total, len(packet))
	}
	if packet[total-2] != 0x0d || packet[total-1] != 0x0a {
		return nil, fmt.Errorf("missing 0x0D 0x0A stop bits")
	}

	content := packet[4 : total-6]
	p := &parsedPacket{
		Protocol: int(packet[3]),
		Content:  content,
		SerialNo: binary.BigEndian.Uint16(packet[total-6 : total-4]),
	}
	crcReceived := binary.BigEndian.Uint16(packet[total-4 : total-2])
	p.CRCValid = crcReceived == crc16Itu(packet, 2, total-4)

	switch p.Protocol {
	case protoLogin:
		if len(content) >= 8 {
			p.IMEI = decodeBcdImei(content[0:8])
		}
	case protoLocation:
		if len(content) >= 18 {
			gps := decodeGpsBlock(content, tzOffsetHours)
			gps.LBS = decodeLbs(content, 18) // no LBS-length byte in 0x12
			p.GPS = &gps
		}
	case protoAlarm:
		if len(content) >= 18 {
			gps := decodeGpsBlock(content, tzOffsetHours)
			// Alarm packets insert a 1-byte LBS length before the LBS block (§5.3.1).
			gps.LBS = decodeLbs(content, 19)
			si := decodeStatusInfo(content, 27)
			p.StatusInfo = si
			if si != nil {
				gps.HasIgnition = true
				gps.Ignition = si.Ignition
				p.Events = eventsFromAlarm(si.AlarmFormer, si)
			}
			p.GPS = &gps
		}
	case protoStatus:
		p.StatusInfo = decodeStatusInfo(content, 0)
	}

	return p, nil
}

// buildResponse builds the 5-byte server response for a protocol + serial number.
// Layout: 78 78 | 05 | protocol | serial(2) | crc(2) | 0D 0A.
func buildResponse(protocol int, serialNo uint16) []byte {
	body := []byte{0x05, byte(protocol), byte(serialNo >> 8), byte(serialNo)}
	crc := crc16Itu(body, 0, len(body))
	out := make([]byte, 0, 10)
	out = append(out, 0x78, 0x78)
	out = append(out, body...)
	out = append(out, byte(crc>>8), byte(crc))
	out = append(out, 0x0d, 0x0a)
	return out
}
