package cathexis

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

// FuzzReadHeader fuzzes the 12-byte little-endian frame header reader.
func FuzzReadHeader(f *testing.F) {
	f.Add(buildHeader(frameJSON, 5))
	f.Add(buildHeader(frameVideo, 1024))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = readHeader(data)
	})
}

// FuzzParseEnvelope fuzzes the JSON control-envelope parser.
func FuzzParseEnvelope(f *testing.F) {
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{"type": "gps", "payload": map[string]any{"latitude": -29.8}},
	})
	f.Add(body)
	f.Add([]byte(`{"message":{"type":"event","payload":{"event":["panic"]}}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseEnvelope(data)
	})
}

// FuzzParseVideoFrame fuzzes the video sub-header parser (reads a device-supplied
// metaSize offset — the one spot that indexes by an attacker-controlled value).
func FuzzParseVideoFrame(f *testing.F) {
	p := make([]byte, videoHdrV1+3)
	binary.LittleEndian.PutUint32(p[0:4], magicVideoV1)
	binary.LittleEndian.PutUint32(p[4:8], 1)   // camera
	binary.LittleEndian.PutUint32(p[32:36], 1) // frameType = I
	p[videoHdrV1], p[videoHdrV1+1], p[videoHdrV1+2] = 0xAA, 0xBB, 0xCC
	f.Add(p)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseVideoFrame(data)
	})
}

// FuzzParseClipChunk fuzzes the recorded-clip chunk parser.
func FuzzParseClipChunk(f *testing.F) {
	p := make([]byte, clipHdrSize+4)
	binary.LittleEndian.PutUint32(p[0:4], magicClip)
	binary.LittleEndian.PutUint32(p[8:12], 1)           // profile
	binary.LittleEndian.PutUint32(p[12:16], 1750000000) // start_utc
	binary.LittleEndian.PutUint32(p[16:20], 1750000020) // end_utc
	binary.LittleEndian.PutUint32(p[28:32], 1)          // start_chunk
	f.Add(p)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseClipChunk(data)
	})
}
