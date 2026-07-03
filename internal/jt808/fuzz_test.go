package jt808

import (
	"bufio"
	"bytes"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
)

// FuzzReadFrame ensures the frame reader never panics on arbitrary bytes.
func FuzzReadFrame(f *testing.F) {
	f.Add(buildFrame(msgLocation, "96750", 1, buildLocationBody(0, 1<<1, 1000000, 2000000, 0, 100, 0, time.Unix(1750000000, 0))))
	f.Add([]byte{flag, 0x01, 0x02, flag})
	f.Add([]byte{flag, escByte, escFlag, flag})
	f.Add([]byte{0x00, 0x7e, 0x7e, 0x7d, 0x01, 0x7e})
	p := &Protocol{}
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bufio.NewReader(bytes.NewReader(data))
		for i := 0; i < 64; i++ {
			f, err := p.ReadFrame(r)
			if err != nil {
				return
			}
			// Header parsing must also be panic-free on whatever passed the checksum.
			_, _, _ = parseHeader(f.Payload)
		}
	})
}

// FuzzReadJT1078Frame ensures the media-stream frame reader never panics.
func FuzzReadJT1078Frame(f *testing.F) {
	f.Add(buildJT1078Video("100000000327", 1, 0, ptH264, []byte{0, 0, 0, 1, 0x65, 0xaa}))
	f.Add([]byte{0x30, 0x31, 0x63, 0x64})
	f.Add([]byte{0x30, 0x31, 0x63, 0x64, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bufio.NewReader(bytes.NewReader(data))
		for i := 0; i < 32; i++ {
			if _, err := readJT1078Frame(r); err != nil {
				return
			}
		}
	})
}

// FuzzReassemble drives the subpackage reassembler with attacker-shaped headers to
// ensure it never panics and never buffers more than maxReasmBytes regardless of the
// advertised SubTotal / SubIndex sequence (the unauthenticated-OOM guard).
func FuzzReassemble(f *testing.F) {
	f.Add(uint16(3), uint16(1), []byte("abc"))
	f.Add(uint16(0xFFFF), uint16(1), bytes.Repeat([]byte{0x41}, maxFrameBytes))
	f.Add(uint16(2), uint16(5), []byte{0x00})
	f.Fuzz(func(t *testing.T, total, startIdx uint16, body []byte) {
		if len(body) > maxFrameBytes {
			body = body[:maxFrameBytes] // frames are size-capped before reaching reassemble
		}
		s := &session{proto: New(N62()), conn: &gateway.Conn{Deps: gateway.Deps{Log: logging.New("test")}}}
		idx := startIdx
		for i := 0; i < 512; i++ {
			h := header{MsgID: msgUlvParamResp, Subpackage: true, SubTotal: total, SubIndex: idx}
			s.reassemble(h, body)
			if b := s.frameReasm[msgUlvParamResp]; b != nil && b.bytes > maxReasmBytes {
				t.Fatalf("reassembly buffer exceeded cap: %d > %d", b.bytes, maxReasmBytes)
			}
			idx++
		}
	})
}

// FuzzParseLocation ensures the location decoder never panics on arbitrary bytes.
func FuzzParseLocation(f *testing.F) {
	f.Add(buildLocationBody(0, 1<<1, 1000000, 2000000, 0, 100, 0, time.Unix(1750000000, 0)))
	f.Add([]byte{})
	f.Add(make([]byte, 28))
	f.Fuzz(func(t *testing.T, data []byte) {
		if loc, ok := parseLocation(data, 0); ok {
			_, _ = testProto.buildLocationPayload(loc, deviceModel)
			_ = testProto.resolveEvents(loc, deviceModel)
		}
		_ = splitBatch(data)
	})
}
