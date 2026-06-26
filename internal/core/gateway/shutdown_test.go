package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/logging"
)

// TestGracefulShutdownInterruptsIdleConn: an idle connection parked in ReadFrame
// must not delay shutdown. The idle timeout is set deliberately long (5 min), so
// without the read-deadline watcher Serve would block in wg.Wait() until that
// deadline — and the test would hit its 5s ceiling and fail.
func TestGracefulShutdownInterruptsIdleConn(t *testing.T) {
	deps := Deps{
		Config: config.Config{ListenHost: "127.0.0.1"},
		Log:    logging.New("test"),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(&fakeProto{name: "p"}, deps)
	srv.SetIdleTimeout(5 * time.Minute) // would stall shutdown for minutes without the fix

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // let handle() reach the blocking ReadFrame

	cancel() // begin graceful shutdown

	select {
	case <-done:
		// Serve returned promptly — the idle connection was interrupted.
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return within 5s of cancel; idle connection blocked shutdown")
	}
}
