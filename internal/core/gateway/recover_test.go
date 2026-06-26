package gateway

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/message"
)

// panicProto is a newline-framed unit whose session panics in OnFrame when it
// receives boomOnFrame, and otherwise registers the serial and emits. Used to
// prove a panic on the device-input path is contained to its one connection.
type panicProto struct {
	name        string
	boomOnFrame string
}

func (p *panicProto) Name() string               { return p.name }
func (p *panicProto) Capabilities() Capabilities { return Capabilities{} }
func (p *panicProto) NewSession(c *Conn) Session { return &panicSess{conn: c, proto: p} }
func (p *panicProto) ReadFrame(r *bufio.Reader) (Frame, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return Frame{}, err
	}
	return Frame{Payload: line}, nil
}

type panicSess struct {
	conn  *Conn
	proto *panicProto
}

func (s *panicSess) OnFrame(_ context.Context, f Frame) error {
	serial := strings.TrimSpace(string(f.Payload))
	if serial == "" {
		return nil
	}
	if serial == s.proto.boomOnFrame {
		panic("boom in OnFrame")
	}
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Register(DeviceInfo{Serial: serial, Protocol: s.proto.name, State: "online"}, s)
	}
	s.conn.Emit(serial, s.proto.name, s.proto.name, "gps", map[string]any{"latitude": 1.0, "longitude": 2.0})
	return nil
}
func (s *panicSess) OnClose(context.Context) {}
func (s *panicSess) SendCommand(context.Context, Command) (CommandResult, error) {
	return CommandResult{}, ErrUnsupportedCommand
}
func (s *panicSess) SupportedCommands() []string { return nil }

// panicSink always panics when it consumes — stands in for a buggy storage sink.
type panicSink struct{}

func (panicSink) Consume(context.Context, message.Inbound, message.Universal) error {
	panic("boom in sink")
}

func startPanicServer(t *testing.T, proto Protocol, sinks []Sink) (string, *Hub) {
	t.Helper()
	hub := NewHub()
	deps := Deps{
		Config:  config.Config{ListenHost: "127.0.0.1"},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("gw", 0),
		Sinks:   sinks,
		Auth:    device.AllowAll{},
		Hub:     hub,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(proto, deps)
	go srv.Serve(ctx, ln)
	return ln.Addr().String(), hub
}

func sendLine(t *testing.T, addr, serial string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(serial + "\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the server process before close
}

func waitRegistered(hub *Hub, serial string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, ok := hub.Get(serial); ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestPanicInOnFrameDoesNotCrashServer: a panic while handling device input is
// contained to that connection; the server keeps serving everyone else. Without
// the recover in handle(), the panic would crash the whole process (and every
// co-hosted unit), failing the test by killing the test binary.
func TestPanicInOnFrameDoesNotCrashServer(t *testing.T) {
	addr, hub := startPanicServer(t, &panicProto{name: "p", boomOnFrame: "BOOM"}, nil)

	sendLine(t, addr, "BOOM") // triggers the panic — must be recovered
	sendLine(t, addr, "GOOD") // a later connection must still work

	if !waitRegistered(hub, "GOOD", 3*time.Second) {
		t.Fatal("server did not survive the OnFrame panic: GOOD never registered")
	}
}

// TestPanicInSinkDoesNotCrashServer: a panic in a fire-and-forget sink goroutine
// (spawned by Emit) is contained by recoverGo, so the process and server survive.
func TestPanicInSinkDoesNotCrashServer(t *testing.T) {
	addr, hub := startPanicServer(t, &panicProto{name: "p"}, []Sink{panicSink{}})

	sendLine(t, addr, "GOOD") // OnFrame emits -> sink goroutine panics, recovered
	if !waitRegistered(hub, "GOOD", 3*time.Second) {
		t.Fatal("GOOD never registered")
	}
	sendLine(t, addr, "GOOD2") // server still alive after the sink panic
	if !waitRegistered(hub, "GOOD2", 3*time.Second) {
		t.Fatal("server did not survive the sink panic")
	}
}
