package jt808

import (
	"bufio"
	"bytes"
	"testing"
)

// bcd6 encodes a 12-digit string as BCD[6] (the JT1078 frame SIM field).
func bcd6(digits string) []byte {
	for len(digits) < 12 {
		digits = "0" + digits
	}
	digits = digits[len(digits)-12:]
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = (digits[2*i]-'0')<<4 | (digits[2*i+1] - '0')
	}
	return out
}

// buildJT1078Video builds a JT1078 video frame (magic + header + body).
func buildJT1078Video(sim string, channel, dataType, pt int, body []byte) []byte {
	out := []byte{0x30, 0x31, 0x63, 0x64}
	out = append(out, 0x81)               // V/P/X/CC flags
	out = append(out, byte(pt&0x7f))      // M|PT
	out = append(out, 0x00, 0x01)         // sequence
	out = append(out, bcd6(sim)...)       // SIM BCD[6]
	out = append(out, byte(channel))      // logical channel
	out = append(out, byte(dataType<<4))  // dataType | subpackage
	out = append(out, make([]byte, 8)...) // timestamp
	out = append(out, 0, 0)               // last I-frame interval
	out = append(out, 0, 0)               // last frame interval
	out = appendU16(out, uint16(len(body)))
	out = append(out, body...)
	return out
}

func TestReadJT1078Frame(t *testing.T) {
	body := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xaa, 0xbb} // pretend H.264 IDR NAL
	frame := buildJT1078Video("100000000327", 1, 0, ptH264, body)
	r := bufio.NewReader(bytes.NewReader(frame))
	f, err := readJT1078Frame(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := serialFromPhone(f.SimDigits); got != "JT808_100000000327" {
		t.Fatalf("serial = %q", got)
	}
	if f.Channel != 1 {
		t.Fatalf("channel = %d, want 1", f.Channel)
	}
	if !f.isVideo() || !f.isKeyframe() {
		t.Fatalf("expected video keyframe, got dataType %d", f.DataType)
	}
	if f.codec() != "h264" {
		t.Fatalf("codec = %s", f.codec())
	}
	if !bytes.Equal(f.Body, body) {
		t.Fatalf("body = %x, want %x", f.Body, body)
	}
}

func TestReadJT1078Resync(t *testing.T) {
	// Prepend garbage; the reader must resync to the magic.
	frame := buildJT1078Video("100000000327", 2, 1, ptH264, []byte{0xde, 0xad})
	stream := append([]byte{0x11, 0x22, 0x33}, frame...)
	r := bufio.NewReader(bytes.NewReader(stream))
	f, err := readJT1078Frame(r)
	if err != nil {
		t.Fatal(err)
	}
	if f.Channel != 2 || f.DataType != 1 {
		t.Fatalf("frame = %+v", f)
	}
}

func TestReadJT1078TwoFrames(t *testing.T) {
	f1 := buildJT1078Video("100000000327", 1, 0, ptH264, []byte{0x01})
	f2 := buildJT1078Video("100000000327", 1, 1, ptH264, []byte{0x02, 0x03})
	r := bufio.NewReader(bytes.NewReader(append(f1, f2...)))
	a, err := readJT1078Frame(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := readJT1078Frame(r)
	if err != nil {
		t.Fatal(err)
	}
	if !a.isKeyframe() || b.isKeyframe() {
		t.Fatalf("keyframe flags wrong: a=%v b=%v", a.isKeyframe(), b.isKeyframe())
	}
	if len(b.Body) != 2 {
		t.Fatalf("second body = %x", b.Body)
	}
}

func TestStreamRoutes(t *testing.T) {
	r := newStreamRoutes()
	r.setLive("JT808_1", 1, 0, 1)
	if lr, ok := r.getLive("JT808_1", 1); !ok || lr.camera != 0 || lr.profile != 1 {
		t.Fatalf("live route = %+v ok=%v", lr, ok)
	}
	r.setPlayback("JT808_1", 2, "clip_x")
	if ss, ok := r.getPlayback("JT808_1", 2); !ok || ss != "clip_x" {
		t.Fatalf("playback route = %q ok=%v", ss, ok)
	}
	r.clearLive("JT808_1", 1)
	if _, ok := r.getLive("JT808_1", 1); ok {
		t.Fatal("live route not cleared")
	}
}
