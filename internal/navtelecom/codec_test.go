package navtelecom

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// crc8Bitwise is the spec's alternate (Java) CRC8: poly 0x31, init 0xFF, no
// reflection. Used to independently validate the lookup table in codec.go.
func crc8Bitwise(buf []byte) byte {
	crc := byte(0xFF)
	for _, b := range buf {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 0x31
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func TestCRC8TableMatchesBitwise(t *testing.T) {
	// Single bytes exercise every table entry.
	for i := 0; i < 256; i++ {
		b := []byte{byte(i)}
		if got, want := crc8(b), crc8Bitwise(b); got != want {
			t.Fatalf("crc8(%#x) = %#x, bitwise = %#x", i, got, want)
		}
	}
	// A spread of multi-byte sequences.
	seqs := [][]byte{
		{},
		[]byte("~A"),
		[]byte("~C"),
		{0x7e, 0x41, 0x01, 0x02, 0x03, 0x04},
		{0xff, 0x00, 0xff, 0x00, 0x12, 0x34, 0x56, 0x78, 0x9a},
	}
	for _, s := range seqs {
		if got, want := crc8(s), crc8Bitwise(s); got != want {
			t.Fatalf("crc8(% x) = %#x, bitwise = %#x", s, got, want)
		}
	}
}

func TestNTCBHeaderRoundTrip(t *testing.T) {
	body := []byte("*<S")
	pkt := buildNTCB(0x0A, 0x01, body)
	if len(pkt) != ntcbHeaderLen+len(body) {
		t.Fatalf("packet len = %d, want %d", len(pkt), ntcbHeaderLen+len(body))
	}
	if string(pkt[0:4]) != "@NTC" {
		t.Fatalf("preamble = %q", pkt[0:4])
	}
	hdr, err := parseNTCBHeader(pkt)
	if err != nil {
		t.Fatalf("parseNTCBHeader: %v", err)
	}
	if hdr.RecipientID != 0x0A || hdr.SenderID != 0x01 || hdr.BodyLen != len(body) {
		t.Fatalf("header = %+v", hdr)
	}
	if pkt[14] != xorSum(body) {
		t.Fatalf("CSd = %#x, want %#x", pkt[14], xorSum(body))
	}
	// Corrupting the header must fail the CSp check.
	bad := append([]byte(nil), pkt...)
	bad[5] ^= 0xFF
	if _, err := parseNTCBHeader(bad); err == nil {
		t.Fatal("expected header checksum error")
	}
}

// setBit sets the FLEX mask bit for a 1-based field number (LSB of byte 0 = field 1).
func setBit(bits []byte, field int) {
	bits[(field-1)/8] |= 1 << ((field - 1) % 8)
}

// buildMask returns a mask byte slice covering dataSize fields with the given
// fields present.
func buildMask(dataSize int, fields ...int) []byte {
	bits := make([]byte, (dataSize+7)/8)
	for _, f := range fields {
		setBit(bits, f)
	}
	return bits
}

func TestParseFlexMask(t *testing.T) {
	fields := []int{1, 2, 3, 10, 11, 13}
	bits := buildMask(69, fields...)
	m, err := parseFlexMask(69, bits)
	if err != nil {
		t.Fatalf("parseFlexMask: %v", err)
	}
	if len(m.fields) != len(fields) {
		t.Fatalf("fields = %v, want %v", m.fields, fields)
	}
	want := 4 + 2 + 4 + 4 + 4 + 4 // sizes of 1,2,3,10,11,13
	if m.recordLen != want {
		t.Fatalf("recordLen = %d, want %d", m.recordLen, want)
	}

	// An unsupported (unknown-size) field is refused so we never mis-frame.
	bad := buildMask(255, 200)
	if _, err := parseFlexMask(255, bad); err == nil {
		t.Fatal("expected error for unsupported field 200")
	}
	// An empty mask is refused.
	if _, err := parseFlexMask(69, make([]byte, 9)); err == nil {
		t.Fatal("expected error for empty mask")
	}
}

// testFields is a representative GPS-only record layout used across tests.
var testFields = []int{1, 2, 3, 7, 8, 10, 11, 12, 13, 14, 19, 29, 31}

// encodeRecord builds a record body for testFields with known values.
func encodeRecord() []byte {
	var b []byte
	u32 := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }
	u16 := func(v uint16) { b = binary.LittleEndian.AppendUint16(b, v) }
	u8 := func(v byte) { b = append(b, v) }
	u32(42)                      // 1 record number
	u16(100)                     // 2 event id
	u32(1700000000)              // 3 event time
	u8(20)                       // 7 GSM level
	u8(0b00100110)               // 8 GPS status: valid(bit1)=1, sats=9
	u32(uint32(int32(33422389))) // 10 lat
	u32(uint32(int32(22616063))) // 11 lon
	u32(uint32(int32(2050)))     // 12 height (decimeters)
	u32(math.Float32bits(60.5))  // 13 speed km/h
	u16(270)                     // 14 course
	u16(12000)                   // 19 main voltage mV
	u8(0b00000101)               // 29 discrete in: In1, In3
	u8(0b00000001)               // 31 outputs: O1 on
	return b
}

func TestDecodeRecord(t *testing.T) {
	m, err := parseFlexMask(69, buildMask(69, testFields...))
	if err != nil {
		t.Fatalf("parseFlexMask: %v", err)
	}
	data := encodeRecord()
	if len(data) != m.recordLen {
		t.Fatalf("encoded %d bytes, recordLen %d", len(data), m.recordLen)
	}
	r, err := decodeRecord(m, data)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if r.RecordNum != 42 || r.EventID != 100 || r.EventTime != 1700000000 {
		t.Fatalf("header fields = %+v", r)
	}
	if !r.positioning() {
		t.Fatal("expected valid positioning (field 8 bit 1)")
	}
	if sats, ok := r.satellites(); !ok || sats != 9 {
		t.Fatalf("satellites = %d (%v), want 9", sats, ok)
	}
	if lat := latLonDegrees(r.Lat); lat < 55.7039 || lat > 55.7041 {
		t.Fatalf("lat = %f, want ~55.7040", lat)
	}
	if lon := latLonDegrees(r.Lon); lon < 37.6933 || lon > 37.6935 {
		t.Fatalf("lon = %f, want ~37.6934", lon)
	}
	if r.Speed != 60.5 {
		t.Fatalf("speed = %f, want 60.5", r.Speed)
	}
	if !r.HasMainVoltage || r.MainVoltage != 12000 {
		t.Fatalf("main voltage = %d (%v)", r.MainVoltage, r.HasMainVoltage)
	}
	if ts, ok := r.utc(); !ok || ts != 1700000000 {
		t.Fatalf("utc = %d (%v)", ts, ok)
	}

	// Wrong length is an error, not a panic.
	if _, err := decodeRecord(m, data[:len(data)-1]); err == nil {
		t.Fatal("expected length error")
	}
}

func TestBuildPayload(t *testing.T) {
	m, _ := parseFlexMask(69, buildMask(69, testFields...))
	r, _ := decodeRecord(m, encodeRecord())
	p := buildPayload("863151075601887", r)

	if p["imei"] != "863151075601887" {
		t.Fatalf("imei = %v", p["imei"])
	}
	if lat, _ := p["latitude"].(float64); lat < 55.7039 || lat > 55.7041 {
		t.Fatalf("latitude = %v", p["latitude"])
	}
	if p["positioning"] != float64(1) {
		t.Fatalf("positioning = %v, want 1", p["positioning"])
	}
	if p["speed"] != 60.5 {
		t.Fatalf("speed = %v", p["speed"])
	}
	if p["bearing"] != float64(270) {
		t.Fatalf("bearing = %v", p["bearing"])
	}
	if p["satellites"] != float64(9) {
		t.Fatalf("satellites = %v", p["satellites"])
	}
	if p["altitude"] != float64(205) {
		t.Fatalf("altitude = %v, want 205", p["altitude"])
	}
	if p["signal"] != float64(20) {
		t.Fatalf("signal = %v", p["signal"])
	}
	an, ok := p["an_inputs"].([]any)
	if !ok || len(an) == 0 || an[0] != float64(12000) {
		t.Fatalf("an_inputs = %v, want main voltage first", p["an_inputs"])
	}
	in, ok := p["inputs"].([]any)
	if !ok || len(in) != 8 || in[0] != float64(1) || in[2] != float64(1) || in[1] != float64(0) {
		t.Fatalf("inputs = %v, want In1 & In3 set", p["inputs"])
	}
	out, ok := p["outputs"].([]any)
	if !ok || out[0] != float64(1) {
		t.Fatalf("outputs = %v, want O1 set", p["outputs"])
	}
}

func TestParseFlexNegotiation(t *testing.T) {
	body := buildFlexNegotiationBody(0x14, 0x14, 69, testFields...)
	protoVer, structVer, m, err := parseFlexNegotiation(body)
	if err != nil {
		t.Fatalf("parseFlexNegotiation: %v", err)
	}
	if protoVer != 0x14 || structVer != 0x14 {
		t.Fatalf("versions = %#x/%#x", protoVer, structVer)
	}
	if m.recordLen != 34 {
		t.Fatalf("recordLen = %d, want 34", m.recordLen)
	}
	if capVersion(structVer) != flexVer10 {
		t.Fatalf("capVersion(%#x) = %#x, want %#x", structVer, capVersion(structVer), flexVer10)
	}

	if !strings.HasPrefix(string(body), "*>FLEX") {
		t.Fatal("negotiation body should start *>FLEX")
	}
}

// buildFlexNegotiationBody builds a `*>FLEX` NTCB body for tests.
func buildFlexNegotiationBody(protoVer, structVer byte, dataSize int, fields ...int) []byte {
	body := []byte("*>FLEX")
	body = append(body, flexProtocol, protoVer, structVer, byte(dataSize))
	body = append(body, buildMask(dataSize, fields...)...)
	return body
}
