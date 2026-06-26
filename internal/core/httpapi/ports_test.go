package httpapi

import (
	"net"
	"testing"
)

// TestPortListening checks the loopback self-dial: true while a listener is open
// on the port, false once it's closed.
func TestPortListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if !portListening(port) {
		t.Errorf("portListening(%d) = false while listener is open, want true", port)
	}

	ln.Close()
	if portListening(port) {
		t.Errorf("portListening(%d) = true after close, want false", port)
	}
}
