package navtelecom

import (
	"bufio"
	"bytes"
	"testing"
)

// FuzzReadFrame feeds arbitrary bytes through the framing layer. ReadFrame parses
// untrusted device input, so it must never panic — it should only ever return a
// frame or an error.
func FuzzReadFrame(f *testing.F) {
	f.Add([]byte("@NTC"))
	f.Add(buildNTCB(1, 0, []byte("*>S:863151075601887")))
	f.Add(buildNTCB(1, 0, buildFlexNegotiationBody(flexVer10, flexVer10, 69, testFields...)))
	f.Add([]byte{markerPing})
	f.Add([]byte{markerFLEX, flexArray, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		p := New()
		r := bufio.NewReader(bytes.NewReader(data))
		// Read frames until the stream is exhausted or errors; just must not panic.
		for i := 0; i < 64; i++ {
			if _, err := p.ReadFrame(r); err != nil {
				break
			}
		}
	})
}

// FuzzDecodeRecord feeds arbitrary bytes into the record decoder under a fixed
// mask. It must never panic regardless of input.
func FuzzDecodeRecord(f *testing.F) {
	m, err := parseFlexMask(69, buildMask(69, testFields...))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encodeRecord())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != m.recordLen {
			// decodeRecord requires exact length; pad/truncate to exercise the body.
			fixed := make([]byte, m.recordLen)
			copy(fixed, data)
			data = fixed
		}
		rec, err := decodeRecord(m, data)
		if err != nil {
			return
		}
		_ = buildPayload("863151075601887", rec) // must not panic either
	})
}
