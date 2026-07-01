package jt808

import "testing"

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
