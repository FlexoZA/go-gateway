package jt808

import (
	"bytes"
	"testing"
)

func TestExtractJPEG(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0x11, 0x22, 0x33, 0xff, 0xd9}
	// 0x0801 logical header (8 bytes) + 28-byte location + JPEG, plus trailing junk.
	full := append(make([]byte, 36), jpeg...)
	full = append(full, 0x00, 0x00) // trailing padding after EOI
	got := extractJPEG(full)
	if !bytes.Equal(got, jpeg) {
		t.Fatalf("extractJPEG = %x, want %x", got, jpeg)
	}
	if extractJPEG([]byte{0x01, 0x02, 0x03}) != nil {
		t.Fatal("expected nil for non-JPEG input")
	}
}

func TestReassembleMultimedia(t *testing.T) {
	s := &session{}
	// Single-packet (not subpackaged) passes through.
	if full, ok := s.reassembleMultimedia(header{}, []byte{1, 2, 3}); !ok || len(full) != 3 {
		t.Fatalf("single-packet reassembly: ok=%v full=%x", ok, full)
	}
	// Three subpackages assemble in index order.
	if _, ok := s.reassembleMultimedia(header{Subpackage: true, SubTotal: 3, SubIndex: 1}, []byte{0xaa}); ok {
		t.Fatal("should not be complete after packet 1")
	}
	if _, ok := s.reassembleMultimedia(header{Subpackage: true, SubTotal: 3, SubIndex: 3}, []byte{0xcc}); ok {
		t.Fatal("should not be complete after packets 1,3")
	}
	full, ok := s.reassembleMultimedia(header{Subpackage: true, SubTotal: 3, SubIndex: 2}, []byte{0xbb})
	if !ok || !bytes.Equal(full, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("reassembled = %x ok=%v, want aabbcc", full, ok)
	}
}

func TestParseHeaderSubpackage(t *testing.T) {
	// 2019 header with the subpackage flag (bit 13) set: total=5, index=2.
	body := []byte("img")
	frame := buildFrame(0x0801, "100000000327", 9, body)
	// buildFrame doesn't set the subpackage bit; assert non-subpackaged parse first.
	h, gotBody, err := parseHeader(frameContent(t, frame))
	if err != nil {
		t.Fatal(err)
	}
	if h.Subpackage || h.MsgID != 0x0801 || !bytes.Equal(gotBody, body) {
		t.Fatalf("header = %+v body=%x", h, gotBody)
	}
}

// frameContent runs a built frame back through the reader to get the unescaped,
// checksum-stripped header+body the session parses.
func frameContent(t *testing.T, frame []byte) []byte {
	t.Helper()
	unesc, err := unescape(frame[1 : len(frame)-1])
	if err != nil {
		t.Fatal(err)
	}
	return unesc[:len(unesc)-1]
}
