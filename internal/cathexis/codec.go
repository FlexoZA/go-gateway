package cathexis

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
)

// Cathexis MVR wire format (ported from dfm-mvr-gateway src/tcp/*.js).
//
// Every message on every connection (control + media) is a 12-byte little-endian
// header — magic 0x12ab34cd, type uint32, size uint32 — followed by `size` bytes
// of payload. Frame types:
//   0  heartbeat (no payload)
//   1  JSON: {"message":{"type":..,"payload":{..}}} (welcome/gps/event/command resp)
//   2  live video (H.264) with a FACEDEAD/FACEDEAE sub-header
//   3  live audio (AAC) — ignored in v1
//   5  clip upload chunk (finished MP4) with a 0xDEAF1234 sub-header
//   15 event-preview JPEG — ignored in v1
//   16 ACK (control welcome ack; size 0)
const (
	headerSize = 12
	magic      = 0x12ab34cd

	frameHeartbeat    = 0
	frameJSON         = 1
	frameVideo        = 2
	frameAudio        = 3
	frameClip         = 5
	frameEventPreview = 15
	frameAck          = 16

	// Video sub-header magics + sizes.
	magicVideoV1  = 0xFACEDEAD
	magicVideoV2  = 0xFACEDEAE
	videoHdrV1    = 44
	videoHdrV2    = 56
	videoFrameKey = 1 // frameType field: 1 = I-frame (keyframe)

	// Clip sub-header (type 5).
	magicClip     = 0xDEAF1234
	clipHdrSize   = 36
	maxFramePayld = 16 * 1024 * 1024 // 16 MiB guardrail per frame
)

// header is a decoded 12-byte frame header.
type header struct {
	Type int
	Size int
}

func readHeader(buf []byte) (header, error) {
	if len(buf) < headerSize {
		return header{}, fmt.Errorf("short header")
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != magic {
		return header{}, fmt.Errorf("bad cathexis magic: 0x%x", binary.LittleEndian.Uint32(buf[0:4]))
	}
	return header{
		Type: int(binary.LittleEndian.Uint32(buf[4:8])),
		Size: int(binary.LittleEndian.Uint32(buf[8:12])),
	}, nil
}

func buildHeader(frameType, size int) []byte {
	out := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(out[0:4], magic)
	binary.LittleEndian.PutUint32(out[4:8], uint32(frameType))
	binary.LittleEndian.PutUint32(out[8:12], uint32(size))
	return out
}

// buildAck is the control-channel ack (type 16, no payload).
func buildAck() []byte { return buildHeader(frameAck, 0) }

// command wraps a control command in the MVR5 envelope and frames it as type-1
// JSON: {"message":{"type":cmdType,"payload":payload}}.
func buildCommand(cmdType string, payload map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{"type": cmdType, "payload": payload},
	})
	return append(buildHeader(frameJSON, len(body)), body...)
}

// envelope is the parsed type-1 JSON message.
type envelope struct {
	Type    string
	Payload map[string]any
}

func parseEnvelope(payload []byte) (envelope, bool) {
	var wrap struct {
		Message struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &wrap); err != nil {
		return envelope{}, false
	}
	if wrap.Message.Payload == nil {
		wrap.Message.Payload = map[string]any{}
	}
	return envelope{Type: wrap.Message.Type, Payload: wrap.Message.Payload}, true
}

// videoFrame is a decoded type-2 video payload: the H.264 access unit plus whether
// it is a keyframe and which camera/profile it belongs to.
type videoFrame struct {
	Camera   int
	Profile  int
	Keyframe bool
	Data     []byte
}

func parseVideoFrame(payload []byte) (videoFrame, bool) {
	if len(payload) < videoHdrV1 {
		return videoFrame{}, false
	}
	m := binary.LittleEndian.Uint32(payload[0:4])
	hdr := videoHdrV1
	switch m {
	case magicVideoV1:
		hdr = videoHdrV1
	case magicVideoV2:
		hdr = videoHdrV2
	default:
		return videoFrame{}, false
	}
	if len(payload) < hdr {
		return videoFrame{}, false
	}
	camera := int(binary.LittleEndian.Uint32(payload[4:8]))
	profile := int(binary.LittleEndian.Uint32(payload[8:12]))
	frameType := int(binary.LittleEndian.Uint32(payload[32:36]))
	metaSize := int(binary.LittleEndian.Uint32(payload[40:44]))
	start := hdr + metaSize
	if start > len(payload) {
		return videoFrame{}, false
	}
	return videoFrame{
		Camera:   camera,
		Profile:  profile,
		Keyframe: frameType == videoFrameKey,
		Data:     payload[start:],
	}, true
}

// clipChunk is a decoded type-5 clip-upload payload: a slice of the device's MP4
// plus the window/identity fields and start/end markers.
type clipChunk struct {
	Camera     int
	Profile    int
	StartUTC   int64
	EndUTC     int64
	FileSize   int64
	StartChunk bool
	EndChunk   bool
	Data       []byte
}

func parseClipChunk(payload []byte) (clipChunk, bool) {
	if len(payload) < clipHdrSize {
		return clipChunk{}, false
	}
	if binary.LittleEndian.Uint32(payload[0:4]) != magicClip {
		return clipChunk{}, false
	}
	return clipChunk{
		Camera:     int(binary.LittleEndian.Uint32(payload[4:8])),
		Profile:    int(binary.LittleEndian.Uint32(payload[8:12])),
		StartUTC:   int64(binary.LittleEndian.Uint32(payload[12:16])),
		EndUTC:     int64(binary.LittleEndian.Uint32(payload[16:20])),
		FileSize:   int64(binary.LittleEndian.Uint32(payload[24:28])),
		StartChunk: binary.LittleEndian.Uint32(payload[28:32]) == 1,
		EndChunk:   binary.LittleEndian.Uint32(payload[32:36]) == 1,
		Data:       payload[clipHdrSize:],
	}, true
}

// liveSessionID is the media-manager session id for a live stream, derived
// identically on the control side (StartLive) and the media side (video frame).
func liveSessionID(serial string, camera, profile int) string {
	return fmt.Sprintf("live_%s_%d_%d", serial, camera, profile)
}

// toFloat coerces a JSON value (number or numeric string, incl. "4,52") to a
// float64. Cathexis devices sometimes send numbers as strings.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		s := t
		// some firmware uses a comma decimal separator
		for i := 0; i < len(s); i++ {
			if s[i] == ',' {
				s = s[:i] + "." + s[i+1:]
				break
			}
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
