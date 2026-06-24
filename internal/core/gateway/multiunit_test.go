package gateway

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/message"
)

// fakeProto is a minimal newline-framed unit used to exercise the multi-unit
// composition: two of these share one Hub and one sink but bind separate ports.
type fakeProto struct{ name string }

func (p *fakeProto) Name() string               { return p.name }
func (p *fakeProto) Capabilities() Capabilities { return Capabilities{} }
func (p *fakeProto) NewSession(c *Conn) Session { return &fakeSess{conn: c, name: p.name} }
func (p *fakeProto) ReadFrame(r *bufio.Reader) (Frame, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: 0, Payload: line}, nil
}

type fakeSess struct {
	conn *Conn
	name string
}

func (s *fakeSess) OnFrame(_ context.Context, f Frame) error {
	serial := strings.TrimSpace(string(f.Payload))
	if serial == "" {
		return nil
	}
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Register(DeviceInfo{Serial: serial, Protocol: s.name, State: "online"}, s)
	}
	s.conn.Emit(serial, s.name, s.name, "gps", map[string]any{"latitude": 1.0, "longitude": 2.0})
	return nil
}
func (s *fakeSess) OnClose(context.Context) {}
func (s *fakeSess) SendCommand(context.Context, Command) (CommandResult, error) {
	return CommandResult{}, ErrUnsupportedCommand
}
func (s *fakeSess) SupportedCommands() []string { return nil }

// capSink records the inbound messages it consumes for assertions.
type capSink struct {
	mu  sync.Mutex
	got []message.Inbound
}

func (c *capSink) Consume(_ context.Context, in message.Inbound, _ message.Universal) error {
	c.mu.Lock()
	c.got = append(c.got, in)
	c.mu.Unlock()
	return nil
}

func (c *capSink) byMake(make string) (message.Inbound, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, in := range c.got {
		if in.Make == make {
			return in, true
		}
	}
	return message.Inbound{}, false
}

// TestMultiUnitSharedHubAndPorts starts two units on separate listeners that share
// one Hub and one sink, and verifies: both devices register in the shared Hub with
// their own protocol, and each unit's emitted message reports its own port (proving
// the per-unit Deps clone).
func TestMultiUnitSharedHubAndPorts(t *testing.T) {
	hub := NewHub()
	sink := &capSink{}
	builder := message.NewBuilder("gw", 0)

	mkDeps := func(port int) Deps {
		return Deps{
			Config:  config.Config{ListenHost: "127.0.0.1", ListenPort: port},
			Log:     logging.New("test"),
			Builder: builder,
			Sinks:   []Sink{sink},
			Auth:    device.AllowAll{},
			Hub:     hub,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// "unita" reports port 8050, "unitb" reports port 33000 (independent of the
	// random bind ports below — Emit uses Deps.Config.ListenPort).
	type unit struct {
		name string
		port int
	}
	units := []unit{{"unita", 8050}, {"unitb", 33000}}
	addrs := map[string]string{}
	for _, u := range units {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := New(&fakeProto{name: u.name}, mkDeps(u.port))
		go srv.Serve(ctx, ln)
		addrs[u.name] = ln.Addr().String()
	}

	send := func(name, serial string) {
		conn, err := net.Dial("tcp", addrs[name])
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(serial + "\n")); err != nil {
			t.Fatal(err)
		}
		// Give the server a moment to process before the deferred close.
		time.Sleep(50 * time.Millisecond)
	}
	send("unita", "AAA")
	send("unitb", "BBB")

	// Both devices land in the shared Hub with the right protocol.
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, okA := hub.Get("AAA")
		b, okB := hub.Get("BBB")
		if okA && okB {
			if a.Protocol != "unita" {
				t.Fatalf("AAA protocol = %q, want unita", a.Protocol)
			}
			if b.Protocol != "unitb" {
				t.Fatalf("BBB protocol = %q, want unitb", b.Protocol)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("devices not both registered: AAA=%v BBB=%v", okA, okB)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Each unit's emitted message reports its own port.
	for {
		ina, okA := sink.byMake("unita")
		inb, okB := sink.byMake("unitb")
		if okA && okB {
			if ina.Port != 8050 {
				t.Fatalf("unita emitted port = %d, want 8050", ina.Port)
			}
			if inb.Port != 33000 {
				t.Fatalf("unitb emitted port = %d, want 33000", inb.Port)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages not both received: unita=%v unitb=%v", okA, okB)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
