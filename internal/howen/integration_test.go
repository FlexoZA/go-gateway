package howen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/message"
	"github.com/dfm/device-gateway/internal/core/webhook"
)

// TestEndToEndAlarmForward drives the full stack: a fake Howen device registers,
// receives the GPS/alarm subscription frames, sends a real alarm packet, and the
// gateway forwards a correct universal webhook message.
func TestEndToEndAlarmForward(t *testing.T) {
	received := make(chan map[string]any, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		received <- msg
		w.WriteHeader(200)
	}))
	defer ts.Close()

	deps := gateway.Deps{
		Config:  config.Config{ListenPort: 33000},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
		Sinks:   []gateway.Sink{webhook.New(ts.URL)},
		Auth:    device.AllowAll{},
		Hub:     gateway.NewHub(),
	}
	srv := gateway.New(New(), deps)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Register a device.
	reg := buildHowenJSONFrame(msgSignalRegister, map[string]any{
		"dn": "864312087845313", "imei": "864312087845313", "fw": "Hero-MC30-02", "ss": "SESS",
	})
	if _, err := conn.Write(reg); err != nil {
		t.Fatal(err)
	}

	// Expect: register response (err 0), gps subscribe, alarm subscribe.
	r := newConnReader(conn)
	for i := 0; i < 3; i++ {
		f := r.read(t)
		switch i {
		case 0:
			if f.Type != msgSignalRegisterResponse {
				t.Fatalf("frame 0 type = %x, want register response", f.Type)
			}
			obj, _ := parseHowenJSONObject(f.Payload)
			if obj["err"] != "0" {
				t.Fatalf("register err = %v, want 0", obj["err"])
			}
		case 1:
			if f.Type != msgGpsSubscribe {
				t.Fatalf("frame 1 type = %x, want gps subscribe", f.Type)
			}
		case 2:
			if f.Type != msgAlarmSubscribe {
				t.Fatalf("frame 2 type = %x, want alarm subscribe", f.Type)
			}
		}
	}

	// Send a real ignition-on alarm packet.
	hexBytes := mustHexBytes(t, liveIgnitionOnAlarmHex)
	alarmFrame := buildHowenFrame(msgAlarmData, hexBytes)
	if _, err := conn.Write(alarmFrame); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-received:
		dev, _ := msg["device"].(map[string]any)
		if dev["serial_no"] != "864312087845313" {
			t.Fatalf("serial_no = %v", dev["serial_no"])
		}
		if dev["type"] != "howen" {
			t.Fatalf("device.type = %v", dev["type"])
		}
		events, _ := msg["events"].([]any)
		if len(events) == 0 {
			t.Fatalf("expected events, got none: %v", msg["events"])
		}
		first, _ := events[0].([]any)
		if len(first) == 0 || first[0] != "IGNITION:ON" {
			t.Fatalf("event = %v, want IGNITION:ON", events)
		}
		gps, _ := msg["gps"].(map[string]any)
		if gps["latitude"] == nil {
			t.Fatalf("expected latitude in gps payload: %v", gps)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	return mustHex(t, s)
}

// TestEndToEndCommand drives the control path: a registered device appears in the
// hub, a command sent "from the HTTP side" reaches the device, and its
// DEVICE_ANSWER resolves the call. Also checks the unsupported/dangerous guards.
func TestEndToEndCommand(t *testing.T) {
	deps := gateway.Deps{
		Config:  config.Config{ListenPort: 33000},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
		Auth:    device.AllowAll{},
		Hub:     gateway.NewHub(),
	}
	hub := deps.Hub
	srv := gateway.New(New(), deps)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const serial = "864312087845313"
	conn.Write(buildHowenJSONFrame(msgSignalRegister, map[string]any{"dn": serial, "fw": "Hero-MC30-02"}))
	r := newConnReader(conn)
	for i := 0; i < 3; i++ { // register response + gps subscribe + alarm subscribe
		r.read(t)
	}

	// Wait until the device is registered in the hub.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := hub.Get(serial); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device never registered in hub")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Send a command from the "HTTP side"; the device must answer for it to return.
	type result struct {
		res gateway.CommandResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		res, err := hub.Send(cctx, serial, gateway.Command{Type: "reboot_unit"})
		resCh <- result{res, err}
	}()

	cmdFrame := r.read(t)
	if cmdFrame.Type != msgRestart {
		t.Fatalf("expected RESTART (0x%x), got 0x%x", msgRestart, cmdFrame.Type)
	}
	obj, _ := parseHowenJSONObject(cmdFrame.Payload)
	ss := toString(obj["ss"])
	if ss == "" {
		t.Fatal("command frame missing session id")
	}
	conn.Write(buildHowenJSONFrame(msgDeviceAnswer, map[string]any{"ss": ss, "err": "0"}))

	select {
	case got := <-resCh:
		if got.err != nil {
			t.Fatalf("command failed: %v", got.err)
		}
		if toString(got.res.Data["err"]) != "0" {
			t.Fatalf("err = %v", got.res.Data["err"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command did not resolve")
	}

	// Unsupported command and unconfirmed dangerous command are rejected locally.
	if _, err := hub.Send(context.Background(), serial, gateway.Command{Type: "nope"}); !errors.Is(err, gateway.ErrUnsupportedCommand) {
		t.Fatalf("want ErrUnsupportedCommand, got %v", err)
	}
	if _, err := hub.Send(context.Background(), serial, gateway.Command{Type: "factory_reset"}); !errors.Is(err, gateway.ErrInvalidCommand) {
		t.Fatalf("want ErrInvalidCommand, got %v", err)
	}
}

// connReader reads framed Howen messages from a net.Conn for assertions.
type connReader struct {
	conn net.Conn
	buf  []byte
}

func newConnReader(c net.Conn) *connReader { return &connReader{conn: c} }

func (cr *connReader) read(t *testing.T) gateway.Frame {
	t.Helper()
	cr.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if len(cr.buf) >= howenHeaderSize {
			h, err := readHowenFrameHeader(cr.buf)
			if err != nil {
				t.Fatalf("header: %v", err)
			}
			total := howenHeaderSize + h.PayloadLength
			if len(cr.buf) >= total {
				payload := append([]byte(nil), cr.buf[howenHeaderSize:total]...)
				cr.buf = cr.buf[total:]
				return gateway.Frame{Type: h.Type, Payload: payload}
			}
		}
		tmp := make([]byte, 4096)
		n, err := cr.conn.Read(tmp)
		if n > 0 {
			cr.buf = append(cr.buf, tmp[:n]...)
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
}
