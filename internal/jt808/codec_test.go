package jt808

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// buildLocationBody builds a 0x0200 location body for tests: 28 fixed bytes then
// the given TLVs appended in order.
func buildLocationBody(alarm, status uint32, latRaw, lonRaw uint32, alt, speed, dir uint16, t time.Time, tlvs ...[]byte) []byte {
	b := make([]byte, 28)
	binary.BigEndian.PutUint32(b[0:4], alarm)
	binary.BigEndian.PutUint32(b[4:8], status)
	binary.BigEndian.PutUint32(b[8:12], latRaw)
	binary.BigEndian.PutUint32(b[12:16], lonRaw)
	binary.BigEndian.PutUint16(b[16:18], alt)
	binary.BigEndian.PutUint16(b[18:20], speed)
	binary.BigEndian.PutUint16(b[20:22], dir)
	copy(b[22:28], bcdTime(t))
	for _, tlv := range tlvs {
		b = append(b, tlv...)
	}
	return b
}

// bcdTime encodes a UTC time as the BCD[6] YYMMDDHHMMSS the device sends.
func bcdTime(t time.Time) []byte {
	t = t.UTC()
	enc := func(n int) byte { return byte((n/10)<<4 | n%10) }
	return []byte{
		enc(t.Year() % 100), enc(int(t.Month())), enc(t.Day()),
		enc(t.Hour()), enc(t.Minute()), enc(t.Second()),
	}
}

func tlv(id byte, value ...byte) []byte {
	return append([]byte{id, byte(len(value))}, value...)
}

func TestEscapeUnescapeRoundTrip(t *testing.T) {
	raw := []byte{0x30, 0x7e, 0x08, 0x7d, 0x55, 0x7e, 0x7d}
	esc := escape(raw)
	if bytes.Contains(esc, []byte{flag}) {
		t.Fatalf("escaped output still contains a raw flag: %x", esc)
	}
	got, err := unescape(esc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("round-trip mismatch: got %x want %x", got, raw)
	}
}

func TestUnescapeBadSequence(t *testing.T) {
	if _, err := unescape([]byte{0x7d, 0x09}); err == nil {
		t.Fatal("expected error on bad escape 0x7d 0x09")
	}
	if _, err := unescape([]byte{0x12, 0x7d}); err == nil {
		t.Fatal("expected error on dangling escape")
	}
}

func TestBCDDigitsAndSerial(t *testing.T) {
	// 0x00..0x96 0x75 0x0 -> "00000000000000096750"
	phone := phoneBCD("96750")
	if got := bcdDigits(phone); got != "00000000000000096750" {
		t.Fatalf("bcdDigits = %q", got)
	}
	if got := serialFromPhone(bcdDigits(phone)); got != "JT808_96750" {
		t.Fatalf("serialFromPhone = %q, want JT808_96750", got)
	}
	if got := serialFromPhone("000000000000"); got != "JT808_0" {
		t.Fatalf("all-zero serial = %q, want JT808_0", got)
	}
}

func TestBuildFrameReadFrameRoundTrip(t *testing.T) {
	body := []byte{0xde, 0xad, 0xbe, 0xef}
	frame := buildFrame(0x0200, "96750", 7, body)
	if frame[0] != flag || frame[len(frame)-1] != flag {
		t.Fatal("frame not flag-delimited")
	}
	p := &Protocol{}
	r := bufio.NewReader(bytes.NewReader(frame))
	f, err := p.ReadFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != 0x0200 {
		t.Fatalf("frame type = 0x%04x, want 0x0200", f.Type)
	}
	h, gotBody, err := parseHeader(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Is2019 || h.Serial != 7 {
		t.Fatalf("header = %+v", h)
	}
	if serialFromPhone(h.Phone) != "JT808_96750" {
		t.Fatalf("phone decode = %q", h.Phone)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body = %x, want %x", gotBody, body)
	}
}

func TestReadFrameDetectsCorruptChecksum(t *testing.T) {
	frame := buildFrame(0x0002, "96750", 1, nil)
	// Flip a header byte (not a flag) so the checksum no longer matches.
	frame[3] ^= 0xff
	p := &Protocol{}
	r := bufio.NewReader(bytes.NewReader(frame))
	if _, err := p.ReadFrame(r); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestParseLocation(t *testing.T) {
	when := time.Date(2026, 1, 27, 11, 29, 26, 0, time.UTC)
	// Gauteng: lat -26.0842 (south), lon 27.9376 (east). status: positioning(bit1)+south(bit2).
	status := uint32(1<<1 | 1<<2)
	body := buildLocationBody(0, status, 26084200, 27937600, 1400, 605, 218, when,
		tlv(0x01, 0x00, 0x00, 0x27, 0x10), // mileage 10000 (1/10 km) -> 1000 km
		tlv(0x30, 22),                     // signal 22
		tlv(0x31, 9),                      // satellites 9
	)
	loc, ok := parseLocation(body, 0)
	if !ok {
		t.Fatal("parseLocation failed")
	}
	if loc.Latitude < -26.0843 || loc.Latitude > -26.0841 {
		t.Fatalf("lat = %v, want ~-26.0842", loc.Latitude)
	}
	if loc.Longitude < 27.9375 || loc.Longitude > 27.9377 {
		t.Fatalf("lon = %v, want ~27.9376", loc.Longitude)
	}
	if loc.Speed != 60.5 {
		t.Fatalf("speed = %v, want 60.5", loc.Speed)
	}
	if loc.Direction != 218 {
		t.Fatalf("direction = %v, want 218", loc.Direction)
	}
	if loc.TimeUTC != when.Unix() {
		t.Fatalf("time = %v, want %v", loc.TimeUTC, when.Unix())
	}
	p, isEvent := buildLocationPayload(loc, deviceModel)
	if isEvent {
		t.Fatal("plain location should not be an event")
	}
	if p["mileage_km"].(float64) != 1000 {
		t.Fatalf("mileage_km = %v, want 1000", p["mileage_km"])
	}
	if p["satellites"].(float64) != 9 {
		t.Fatalf("satellites = %v", p["satellites"])
	}
	if p["positioning"].(bool) != true {
		t.Fatal("positioning should be true")
	}
}

func TestParseLocationTZOffset(t *testing.T) {
	// A device sending local time at GMT+2 should decode back to UTC.
	utc := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	local := utc.Add(2 * time.Hour)
	body := buildLocationBody(0, 1<<1, 1000000, 2000000, 0, 0, 0, local)
	loc, ok := parseLocation(body, 2)
	if !ok {
		t.Fatal("parse failed")
	}
	if loc.TimeUTC != utc.Unix() {
		t.Fatalf("tz-adjusted time = %v, want %v", loc.TimeUTC, utc.Unix())
	}
}

func TestSplitBatch(t *testing.T) {
	inner1 := buildLocationBody(0, 1<<1, 1000000, 2000000, 0, 100, 0, time.Now())
	inner2 := buildLocationBody(0, 1<<1, 1100000, 2100000, 0, 200, 0, time.Now())
	body := make([]byte, 3)
	binary.BigEndian.PutUint16(body[0:2], 2) // count
	body[2] = 0                              // type
	for _, in := range [][]byte{inner1, inner2} {
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(in)))
		body = append(body, l[:]...)
		body = append(body, in...)
	}
	items := splitBatch(body)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if !bytes.Equal(items[0], inner1) || !bytes.Equal(items[1], inner2) {
		t.Fatal("batch items mismatch")
	}
}

func TestParseHeader2013(t *testing.T) {
	// Hand-build a 2013 header (bit14 clear): id(2) attrs(2) phone[6] serial(2) body.
	buf := make([]byte, 12+1)
	binary.BigEndian.PutUint16(buf[0:2], 0x0002)
	binary.BigEndian.PutUint16(buf[2:4], 0x0001) // body len 1, 2013
	copy(buf[4:10], []byte{0x00, 0x00, 0x00, 0x09, 0x67, 0x50})
	binary.BigEndian.PutUint16(buf[10:12], 3)
	buf[12] = 0xaa
	h, body, err := parseHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if h.Is2019 {
		t.Fatal("should be detected as 2013")
	}
	if serialFromPhone(h.Phone) != "JT808_96750" {
		t.Fatalf("phone = %q", h.Phone)
	}
	if len(body) != 1 || body[0] != 0xaa {
		t.Fatalf("body = %x", body)
	}
}
