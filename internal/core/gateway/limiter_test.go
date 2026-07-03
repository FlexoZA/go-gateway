package gateway

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/logging"
)

// TestConnLimiter covers the global and per-IP caps and the per-IP map cleanup.
func TestConnLimiter(t *testing.T) {
	l := newConnLimiter(2, 0) // global cap 2, per-IP disabled
	if !l.acquire("1.1.1.1") || !l.acquire("2.2.2.2") {
		t.Fatal("first two acquires should succeed")
	}
	if l.acquire("3.3.3.3") {
		t.Fatal("third acquire should be refused by the global cap")
	}
	l.release("1.1.1.1")
	if !l.acquire("3.3.3.3") {
		t.Fatal("a slot freed by release should be reusable")
	}

	// Per-IP cap: one IP is limited while another is unaffected.
	p := newConnLimiter(0, 2) // global disabled, per-IP cap 2
	if !p.acquire("9.9.9.9") || !p.acquire("9.9.9.9") {
		t.Fatal("two per-IP acquires should succeed")
	}
	if p.acquire("9.9.9.9") {
		t.Fatal("third acquire from the same IP should be refused")
	}
	if !p.acquire("8.8.8.8") {
		t.Fatal("a different IP must not be affected by another IP's cap")
	}
	// Releasing all of an IP's slots must drop its map entry (no unbounded growth).
	p.release("9.9.9.9")
	p.release("9.9.9.9")
	p.mu.Lock()
	_, present := p.perIP["9.9.9.9"]
	p.mu.Unlock()
	if present {
		t.Fatal("per-IP entry should be deleted once its count reaches zero")
	}
}

// TestServeRejectsOverCap: with a global cap of 1, a second concurrent connection is
// accepted-then-closed by the server while the first is still held open.
func TestServeRejectsOverCap(t *testing.T) {
	deps := Deps{
		Config: config.Config{ListenHost: "127.0.0.1", MaxConnections: 1},
		Log:    logging.New("test"),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(&fakeProto{name: "p"}, deps)
	srv.SetIdleTimeout(5 * time.Minute) // keep the first connection parked in ReadFrame

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	// First connection occupies the single slot and stays open (never sends a frame).
	c1, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	time.Sleep(100 * time.Millisecond) // let handle() acquire the slot

	// Second connection is over the cap: the server closes it, so a read returns EOF.
	c2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(c2).ReadByte(); err == nil {
		t.Fatal("second connection should have been closed by the cap, but read succeeded")
	}

	// The first connection is still alive: a frame it sends is processed (the sink
	// would see it). We just assert the write succeeds and the conn isn't closed.
	if _, err := c1.Write([]byte("SERIAL1\n")); err != nil {
		t.Fatalf("first connection should still be open: %v", err)
	}
}
