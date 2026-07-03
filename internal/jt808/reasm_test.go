package jt808

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
)

// TestReassembleSubpackage: a subpackaged message is only delivered once every
// package has arrived, and the fragment bodies are concatenated in index order
// (not arrival order). The per-MsgID buffer clears after completion.
func TestReassembleSubpackage(t *testing.T) {
	s := &session{}
	mk := func(total, idx uint16) header {
		return header{MsgID: msgUlvParamResp, Subpackage: true, SubTotal: total, SubIndex: idx}
	}

	// Fragments arrive out of order (2, then 1, then 3); only the last completes.
	if full, ok := s.reassemble(mk(3, 2), []byte("bbb")); ok || full != nil {
		t.Fatalf("fragment 2/3 should be incomplete, got ok=%v full=%q", ok, full)
	}
	if _, ok := s.reassemble(mk(3, 1), []byte("aaa")); ok {
		t.Fatalf("fragment 1/3 should still be incomplete")
	}
	full, ok := s.reassemble(mk(3, 3), []byte("ccc"))
	if !ok || string(full) != "aaabbbccc" {
		t.Fatalf("reassembled = %q ok=%v, want %q true", full, ok, "aaabbbccc")
	}

	// A new message (same MsgID) starts from a cleared buffer.
	if _, ok := s.reassemble(mk(2, 1), []byte("xx")); ok {
		t.Fatalf("new message should not complete on its first fragment")
	}
	full, ok = s.reassemble(mk(2, 2), []byte("yy"))
	if !ok || string(full) != "xxyy" {
		t.Fatalf("second message reassembled = %q ok=%v, want %q true", full, ok, "xxyy")
	}
}

// TestReassembleGuards: fragments with out-of-range indices are dropped, and a
// message whose fragments exceed maxReasmBytes is discarded rather than buffered
// without bound (the unauthenticated-OOM guard).
func TestReassembleGuards(t *testing.T) {
	s := &session{proto: New(N62()), conn: &gateway.Conn{Deps: gateway.Deps{Log: logging.New("test")}}}
	mk := func(total, idx uint16) header {
		return header{MsgID: msgUlvParamResp, Subpackage: true, SubTotal: total, SubIndex: idx}
	}

	// SubIndex 0 and SubIndex > SubTotal are malformed and must not be buffered.
	if _, ok := s.reassemble(mk(3, 0), []byte("x")); ok {
		t.Fatalf("SubIndex 0 should be rejected")
	}
	if _, ok := s.reassemble(mk(3, 4), []byte("x")); ok {
		t.Fatalf("SubIndex past SubTotal should be rejected")
	}
	if len(s.frameReasm) != 0 {
		t.Fatalf("rejected fragments must not allocate a buffer, got %d", len(s.frameReasm))
	}

	// A hostile stream advertises a huge SubTotal and dribbles max-size fragments.
	// Once the aggregate would exceed maxReasmBytes the whole reassembly is dropped.
	frag := make([]byte, maxFrameBytes)
	const total = 0xFFFF
	for i := uint16(1); i <= total; i++ {
		full, ok := s.reassemble(mk(total, i), frag)
		if ok {
			t.Fatalf("oversize stream should never complete (fragment %d)", i)
		}
		if full != nil {
			t.Fatalf("oversize stream should not return data")
		}
		if b := s.frameReasm[msgUlvParamResp]; b != nil && b.bytes > maxReasmBytes {
			t.Fatalf("buffer grew past cap: %d > %d", b.bytes, maxReasmBytes)
		}
		// Once it trips the cap the buffer is cleared; further fragments re-fill and
		// trip again, so a few hundred iterations prove boundedness without 65k loops.
		if i > 300 {
			break
		}
	}
}
