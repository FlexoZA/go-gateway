package jt808

import (
	"bufio"
	"bytes"
	"testing"
	"time"
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

// FuzzParseLocation ensures the location decoder never panics on arbitrary bytes.
func FuzzParseLocation(f *testing.F) {
	f.Add(buildLocationBody(0, 1<<1, 1000000, 2000000, 0, 100, 0, time.Unix(1750000000, 0)))
	f.Add([]byte{})
	f.Add(make([]byte, 28))
	f.Fuzz(func(t *testing.T, data []byte) {
		if loc, ok := parseLocation(data, 0); ok {
			_, _ = buildLocationPayload(loc, deviceModel)
			_ = resolveEvents(loc, deviceModel)
		}
		_ = splitBatch(data)
	})
}
