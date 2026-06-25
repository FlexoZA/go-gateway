package cathexis

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	buf := buildHeader(frameJSON, 5)
	h, err := readHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != frameJSON || h.Size != 5 {
		t.Fatalf("header = %+v, want {Type:1 Size:5}", h)
	}
	// Bad magic is rejected.
	bad := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(bad[0:4], 0xdeadbeef)
	if _, err := readHeader(bad); err == nil {
		t.Fatal("expected bad-magic error")
	}
}

func TestParseEnvelope(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{"type": "gps", "payload": map[string]any{"latitude": -29.8}},
	})
	env, ok := parseEnvelope(body)
	if !ok || env.Type != "gps" {
		t.Fatalf("env = %+v ok=%v", env, ok)
	}
	if env.Payload["latitude"].(float64) != -29.8 {
		t.Fatalf("latitude = %v", env.Payload["latitude"])
	}
}

func TestParseVideoFrame(t *testing.T) {
	p := make([]byte, videoHdrV1+3)
	binary.LittleEndian.PutUint32(p[0:4], magicVideoV1)
	binary.LittleEndian.PutUint32(p[4:8], 1)  // camera
	binary.LittleEndian.PutUint32(p[8:12], 0) // profile
	binary.LittleEndian.PutUint32(p[32:36], 1) // frameType = I
	binary.LittleEndian.PutUint32(p[40:44], 0) // metaSize
	p[videoHdrV1], p[videoHdrV1+1], p[videoHdrV1+2] = 0xAA, 0xBB, 0xCC
	vf, ok := parseVideoFrame(p)
	if !ok {
		t.Fatal("parse failed")
	}
	if vf.Camera != 1 || vf.Profile != 0 || !vf.Keyframe || len(vf.Data) != 3 {
		t.Fatalf("vf = %+v", vf)
	}
}

func TestParseClipChunk(t *testing.T) {
	p := make([]byte, clipHdrSize+4)
	binary.LittleEndian.PutUint32(p[0:4], magicClip)
	binary.LittleEndian.PutUint32(p[4:8], 0)            // camera
	binary.LittleEndian.PutUint32(p[8:12], 1)           // profile
	binary.LittleEndian.PutUint32(p[12:16], 1750000000) // start_utc
	binary.LittleEndian.PutUint32(p[16:20], 1750000020) // end_utc
	binary.LittleEndian.PutUint32(p[28:32], 1)          // start_chunk
	binary.LittleEndian.PutUint32(p[32:36], 0)          // end_chunk
	cc, ok := parseClipChunk(p)
	if !ok {
		t.Fatal("parse failed")
	}
	if cc.Camera != 0 || cc.Profile != 1 || cc.StartUTC != 1750000000 || !cc.StartChunk || cc.EndChunk || len(cc.Data) != 4 {
		t.Fatalf("cc = %+v", cc)
	}
}

func TestToStandardEventCodes(t *testing.T) {
	got := toStandardEventCodes(map[string]any{"name": "harsh_braking"}, true)
	if len(got) != 1 || got[0] != "HARSH:BRAKING" {
		t.Fatalf("got %v, want [HARSH:BRAKING]", got)
	}
	got = toStandardEventCodes(map[string]any{"event": []any{"panic", "weird_thing"}}, true)
	if len(got) != 2 || got[0] != "PANIC" || got[1] != "ALARM" {
		t.Fatalf("got %v, want [PANIC ALARM]", got)
	}
	// An event message with nothing recognizable still yields ALARM.
	got = toStandardEventCodes(map[string]any{}, true)
	if len(got) != 1 || got[0] != "ALARM" {
		t.Fatalf("got %v, want [ALARM]", got)
	}
}
