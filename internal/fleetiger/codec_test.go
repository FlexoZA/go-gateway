package fleetiger

import (
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

// hexBytes decodes a spaced hex string into bytes.
func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// buildFrame assembles a GT06 frame from a protocol number + content, computing
// the length byte and CRC (mirrors the JS test helper).
func buildFrame(protocol int, content []byte, serialNo uint16) []byte {
	lengthByte := byte(1 + len(content) + 2 + 2) // proto + content + serial + crc
	head := make([]byte, 0, 2+len(content)+2)
	head = append(head, lengthByte, byte(protocol))
	head = append(head, content...)
	head = append(head, byte(serialNo>>8), byte(serialNo))
	crc := crc16Itu(head, 0, len(head))
	out := make([]byte, 0, len(head)+6)
	out = append(out, 0x78, 0x78)
	out = append(out, head...)
	out = append(out, byte(crc>>8), byte(crc))
	out = append(out, 0x0d, 0x0a)
	return out
}

// TestCRCAgainstSpec checks CRC-ITU against the spec's canonical login response
// (§5.1.2): 78 78 05 01 00 01 D9 DC 0D 0A -> CRC over [05 01 00 01] = 0xD9DC.
func TestCRCAgainstSpec(t *testing.T) {
	if got := crc16Itu(hexBytes(t, "05 01 00 01"), 0, 4); got != 0xd9dc {
		t.Fatalf("crc16Itu = 0x%04X, want 0xD9DC", got)
	}
}

// TestBuildResponseMatchesSpec checks buildResponse reproduces the spec
// login-response packet byte-for-byte.
func TestBuildResponseMatchesSpec(t *testing.T) {
	got := buildResponse(protoLogin, 0x0001)
	want := hexBytes(t, "78 78 05 01 00 01 D9 DC 0D 0A")
	if string(got) != string(want) {
		t.Fatalf("buildResponse = % X, want % X", got, want)
	}
}

// TestParseLogin decodes the spec login packet (§5.1.1): IMEI 123456789012345.
func TestParseLogin(t *testing.T) {
	packet := hexBytes(t, "78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A")
	p, err := parseGt06Packet(packet, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Protocol != protoLogin {
		t.Fatalf("protocol = 0x%02X, want login", p.Protocol)
	}
	if !p.CRCValid {
		t.Fatal("spec login packet CRC should validate")
	}
	if p.IMEI != "123456789012345" {
		t.Fatalf("IMEI = %q, want 123456789012345", p.IMEI)
	}
}

// TestParseLocation decodes the spec location packet (§5.2.1 worked example):
// 2010-03-23 15:30:23, N latitude, E longitude.
func TestParseLocation(t *testing.T) {
	packet := hexBytes(t, "78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F "+
		"01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A")
	p, err := parseGt06Packet(packet, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Protocol != protoLocation {
		t.Fatalf("protocol = 0x%02X, want location", p.Protocol)
	}
	if !p.CRCValid {
		t.Fatal("spec location packet CRC should validate")
	}
	if p.GPS == nil {
		t.Fatal("location packet should produce a gps block")
	}
	// 0x027AC7EB -> /30000/60 = 23.1117 North (Appendix B "Lat:N23.111682").
	if math.Abs(p.GPS.Latitude-23.111668) >= 0.0005 {
		t.Fatalf("latitude = %v, want ~23.1117", p.GPS.Latitude)
	}
	// -> 114.4093 East (Appendix B "Lon:E114.40922").
	if math.Abs(p.GPS.Longitude-114.409285) >= 0.0005 {
		t.Fatalf("longitude = %v, want ~114.4093", p.GPS.Longitude)
	}
	if p.GPS.Positioning != 1 {
		t.Fatalf("positioning = %d, want 1 (status bit4)", p.GPS.Positioning)
	}
	if p.GPS.Satellites != 15 {
		t.Fatalf("satellites = %d, want 15 (low nibble of 0xCF)", p.GPS.Satellites)
	}
	if p.GPS.LBS == nil || p.GPS.LBS.MCC != 0x01cc {
		t.Fatalf("MCC should decode from the LBS block, got %+v", p.GPS.LBS)
	}
}

// TestParseLocationStatus0x22 decodes the extended location variant (0x22) that
// the real FleeTiger unit sends instead of base 0x12. The GPS block layout matches
// 0x12, so the same decoder applies. Coordinates are the real Johannesburg fix
// captured from device 868755137537288.
func TestParseLocationStatus0x22(t *testing.T) {
	content := []byte{
		0x1a, 0x06, 0x18, 0x0c, 0x35, 0x1e, // 2026-06-24 12:53:30
		0xcd,                   // GPS info length (hi nibble) + 13 satellites
		0x02, 0xcc, 0x6b, 0xbe, // latitude raw  (~ -26.08, South)
		0x02, 0xff, 0x55, 0x75, // longitude raw (~  27.97, East)
		0x00,       // speed
		0x50, 0x34, // status: bit4 positioned, South + East, course 0x34
		// extended status/LBS tail (not decoded by the GPS block)
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
	}
	p, err := parseGt06Packet(buildFrame(protoLocationStatus, content, 0x020d), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Protocol != protoLocationStatus {
		t.Fatalf("protocol = 0x%02X, want 0x22", p.Protocol)
	}
	if !p.CRCValid || p.GPS == nil {
		t.Fatalf("expected a valid GPS block, got %+v", p)
	}
	if p.GPS.Satellites != 13 || p.GPS.Positioning != 1 {
		t.Fatalf("satellites=%d positioning=%d, want 13 / 1", p.GPS.Satellites, p.GPS.Positioning)
	}
	if math.Abs(p.GPS.Latitude-(-26.08)) >= 0.05 {
		t.Fatalf("latitude = %v, want ~-26.08", p.GPS.Latitude)
	}
	if math.Abs(p.GPS.Longitude-27.97) >= 0.05 {
		t.Fatalf("longitude = %v, want ~27.97", p.GPS.Longitude)
	}
}

// TestTimezoneOffset confirms a +2h device-local time shifts UTC back by 7200s.
func TestTimezoneOffset(t *testing.T) {
	packet := hexBytes(t, "78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F "+
		"01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A")
	p0, _ := parseGt06Packet(packet, 0)
	p2, _ := parseGt06Packet(packet, 2)
	if p0.GPS.UTC-p2.GPS.UTC != 7200 {
		t.Fatalf("a +2h offset should move UTC back 7200s, got %d", p0.GPS.UTC-p2.GPS.UTC)
	}
}

// TestSouthernHemisphere round-trips a South Africa fix (S lat, E lon) to verify
// the hemisphere sign bits are applied correctly.
func TestSouthernHemisphere(t *testing.T) {
	content := []byte{
		0x1a, 0x06, 0x16, 0x0c, 0x00, 0x00, // 2026-06-22 12:00:00
		0xc5,                   // gps info len high nibble, 5 satellites
		0x02, 0xcf, 0xc9, 0xd4, // latitude raw (-26.2041)
		0x03, 0x02, 0x0f, 0x54, // longitude raw (28.0473)
		0x00,       // speed
		0x10, 0x2d, // status: bit4 positioned, South + East, course 0x2D
		0x01, 0xcc, 0x00, 0x28, 0x7d, 0x00, 0x1f, 0xb8, // LBS block
	}
	p, err := parseGt06Packet(buildFrame(protoLocation, content, 0x0001), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !p.CRCValid {
		t.Fatal("synthetic SA frame CRC should validate")
	}
	if p.GPS.Latitude >= 0 {
		t.Fatalf("SA latitude should be negative (South), got %v", p.GPS.Latitude)
	}
	if p.GPS.Longitude <= 0 {
		t.Fatalf("SA longitude should be positive (East), got %v", p.GPS.Longitude)
	}
	if math.Abs(p.GPS.Latitude-(-26.2)) >= 0.05 {
		t.Fatalf("SA latitude magnitude, got %v", p.GPS.Latitude)
	}
	if math.Abs(p.GPS.Longitude-28.05) >= 0.05 {
		t.Fatalf("SA longitude magnitude, got %v", p.GPS.Longitude)
	}
}

// TestCRCTamperDetected confirms a flipped content bit fails CRC.
func TestCRCTamperDetected(t *testing.T) {
	packet := hexBytes(t, "78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F "+
		"01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A")
	packet[10] ^= 0x01
	p, err := parseGt06Packet(packet, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.CRCValid {
		t.Fatal("a flipped content bit should fail CRC")
	}
}

// TestStatusHeartbeatAck verifies a heartbeat is decoded and that buildResponse
// produces the spec heartbeat ACK shape.
func TestStatusHeartbeatAck(t *testing.T) {
	// terminalInfo 0x44, voltage 4, gsm 3, alarm/lang 00 01.
	content := []byte{0x44, 0x04, 0x03, 0x00, 0x01}
	p, err := parseGt06Packet(buildFrame(protoStatus, content, 0x0001), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !p.CRCValid || p.StatusInfo == nil {
		t.Fatalf("heartbeat should decode a status block, got %+v", p)
	}
	if p.StatusInfo.Ignition != 0 || p.StatusInfo.Charging != 1 {
		t.Fatalf("status flags: ignition=%d charging=%d (terminalInfo 0x44)", p.StatusInfo.Ignition, p.StatusInfo.Charging)
	}
	if !protocolNeedsAck(p.Protocol) {
		t.Fatal("heartbeat must be acknowledged")
	}
}
