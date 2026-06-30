package jt808

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// JT/T 1078 real-time A/V stream framing (the format the N62 dials our media port
// with after a 0x9101/0x9201 request). Each frame begins with the 4-byte magic
// 0x30 0x31 0x63 0x64 ("01cd") followed by a partially-variable header and a body
// of the declared length. Video bodies are raw H.264/H.265 access-unit fragments
// (Annex-B), which feed the core media pipeline directly.
//
// Header (big-endian), per JT/T 1078-2016 Table 19:
//
//	0..3   magic 0x30316364
//	4      V/P/X/CC          (RTP-ish flags; ignored)
//	5      M(1)|PT(7)        PT: 98=H264, 99=H265
//	6..7   sequence number
//	8..13  SIM card BCD[6]   (terminal phone digits)
//	14     logical channel number (1-based)
//	15     dataType(hi nibble)|subpackage(lo nibble)
//	          dataType 0=video I, 1=video P, 2=video B, 3=audio, 4=transparent
//	16..23 timestamp (ms)        — present for dataType 0..3
//	24..25 last I-frame interval — present for dataType 0..2 (video)
//	26..27 last frame interval   — present for dataType 0..2 (video)
//	N..N+1 data body length (the 2 bytes immediately before the body)
//	then   body (length bytes)

const (
	jt1078Magic    = 0x30316364
	ptH264         = 98
	ptH265         = 99
	maxJT1078Body  = 64 * 1024 // body length is a uint16; cap defensively
	maxSyncScanned = 1 << 20   // give up resync after scanning this many bytes
)

// jt1078Frame is one decoded JT1078 stream frame.
type jt1078Frame struct {
	SimDigits string
	Channel   int // 1-based logical channel
	DataType  int // 0=I,1=P,2=B,3=audio,4=transparent
	PT        int // payload type (98=H264, 99=H265)
	Body      []byte
}

func (f jt1078Frame) isVideo() bool    { return f.DataType >= 0 && f.DataType <= 2 }
func (f jt1078Frame) isKeyframe() bool { return f.DataType == 0 }
func (f jt1078Frame) codec() string {
	if f.PT == ptH265 {
		return "hevc"
	}
	return "h264"
}

// syncJT1078 advances r to just past the next 0x30316364 magic.
func syncJT1078(r *bufio.Reader) error {
	var window uint32
	have := 0
	for scanned := 0; scanned < maxSyncScanned; scanned++ {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		window = window<<8 | uint32(b)
		if have < 4 {
			have++
		}
		if have >= 4 && window == jt1078Magic {
			return nil
		}
	}
	return fmt.Errorf("jt808 media: JT1078 magic not found")
}

// readJT1078Frame reads one frame from the media connection. It resyncs to the
// magic first (robust to mid-stream join), then reads the variable header and the
// body.
func readJT1078Frame(r *bufio.Reader) (jt1078Frame, error) {
	if err := syncJT1078(r); err != nil {
		return jt1078Frame{}, err
	}
	// Fixed part after the magic: bytes 4..15 (12 bytes).
	var h [12]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return jt1078Frame{}, err
	}
	f := jt1078Frame{
		PT:        int(h[1] & 0x7f),
		SimDigits: bcdDigits(h[4:10]),
		Channel:   int(h[10]),
		DataType:  int(h[11] >> 4),
	}
	// Variable tail holds the optional timestamp/interval fields then a 2-byte body
	// length. Its size depends on the data type.
	var tail int
	switch {
	case f.DataType <= 2: // video: timestamp(8) + lastIFrame(2) + lastFrame(2) + len(2)
		tail = 14
	case f.DataType == 3: // audio: timestamp(8) + len(2)
		tail = 10
	default: // transparent: len(2)
		tail = 2
	}
	ext := make([]byte, tail)
	if _, err := io.ReadFull(r, ext); err != nil {
		return jt1078Frame{}, err
	}
	bodyLen := int(binary.BigEndian.Uint16(ext[tail-2 : tail]))
	if bodyLen > maxJT1078Body {
		return jt1078Frame{}, fmt.Errorf("jt808 media: body too large (%d)", bodyLen)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return jt1078Frame{}, err
	}
	f.Body = body
	return f, nil
}
