package cathexis

import (
	"context"
	"encoding/json"
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

// dialAndWelcome starts a cathexis server, connects, sends a welcome, and waits
// for the device to register in the hub. Returns the live connection + serial.
func dialAndWelcome(t *testing.T, deps gateway.Deps) (net.Conn, string) {
	t.Helper()
	srv := gateway.New(New(), deps)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Write(buildCommand("welcome", map[string]any{"serial": "MVR5452 4064668"})); err != nil {
		t.Fatal(err)
	}
	const serial = "MVR5452_4064668"
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := deps.Hub.Get(serial); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device never registered in hub")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return conn, serial
}

func newDeps(t *testing.T, received chan map[string]any) gateway.Deps {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		received <- msg
		w.WriteHeader(200)
	}))
	t.Cleanup(ts.Close)
	return gateway.Deps{
		Config:  config.Config{ListenPort: 32324},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
		Sinks:   []gateway.Sink{webhook.New(ts.URL)},
		Auth:    device.AllowAll{},
		Hub:     gateway.NewHub(),
	}
}

// TestGpsForward: a welcome then a GPS message is forwarded as a universal message
// with the cathexis device type and speed converted m/s → km/h.
func TestGpsForward(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	conn, serial := dialAndWelcome(t, deps)

	conn.Write(buildCommand("gps", map[string]any{
		"latitude": -29.8442, "longitude": 30.9129, "speed": 10.0, // 10 m/s -> 36 km/h
		"bearing": 90, "satellites": 12, "utc": 1750000000,
	}))

	select {
	case msg := <-received:
		dev, _ := msg["device"].(map[string]any)
		if dev["serial_no"] != serial {
			t.Fatalf("serial_no = %v, want %s", dev["serial_no"], serial)
		}
		if dev["type"] != "cathexis" {
			t.Fatalf("device.type = %v, want cathexis", dev["type"])
		}
		gps, _ := msg["gps"].(map[string]any)
		if lat, _ := gps["latitude"].(float64); lat < -29.85 || lat > -29.83 {
			t.Fatalf("gps.latitude = %v, want ~-29.84", gps["latitude"])
		}
		if spd, _ := gps["speed"].(float64); spd != 36 {
			t.Fatalf("gps.speed = %v, want 36 (10 m/s -> km/h)", gps["speed"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for gps webhook")
	}
}

// TestEventConnectionNotRegistered: a non-control connection (connection_type
// "event") is approved but must NOT register in the hub, or it would clobber the
// control connection's command channel.
func TestEventConnectionNotRegistered(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	srv := gateway.New(New(), deps)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.Write(buildCommand("welcome", map[string]any{
		"serial": "MVR5452 4064668", "connection_type": "event",
	}))

	// Give the server time to process; the device must not appear in the hub.
	time.Sleep(300 * time.Millisecond)
	if _, ok := deps.Hub.Get("MVR5452_4064668"); ok {
		t.Fatal("event connection should not register in the hub")
	}
}

// TestStandbyLifecycle: a power-state event flips the hub device state between
// online and sleep.
func TestStandbyLifecycle(t *testing.T) {
	received := make(chan map[string]any, 8)
	deps := newDeps(t, received)
	conn, serial := dialAndWelcome(t, deps)

	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if info, ok := deps.Hub.Get(serial); ok && info.State == want {
				return
			}
			if time.Now().After(deadline) {
				info, _ := deps.Hub.Get(serial)
				t.Fatalf("state never became %q (got %q)", want, info.State)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	conn.Write(buildCommand("event", map[string]any{"name": "entered_standby", "utc": 1750000100}))
	waitState("sleep")
	conn.Write(buildCommand("event", map[string]any{"name": "ignition_on", "utc": 1750000200}))
	waitState("online")
}

// readClientFrame reads one framed message the server sent to the client conn.
func readClientFrame(t *testing.T, conn net.Conn) gateway.Frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hdr [headerSize]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	h, err := readHeader(hdr[:])
	if err != nil {
		t.Fatalf("bad header: %v", err)
	}
	payload := make([]byte, h.Size)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return gateway.Frame{Type: h.Type, Payload: payload}
}

// TestWakeDevice: wake_device is advertised and sends a lightweight dAPI request
// (request_sd_health) over the control socket to poke the unit out of standby.
func TestWakeDevice(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	conn, serial := dialAndWelcome(t, deps)

	res, err := deps.Hub.Send(context.Background(), serial, gateway.Command{Type: "wake_device"})
	if err != nil {
		t.Fatalf("wake_device: %v", err)
	}
	if ok, _ := res.Data["ok"].(bool); !ok {
		t.Fatalf("wake result = %+v, want ok", res.Data)
	}
	f := readClientFrame(t, conn)
	if f.Type != frameJSON {
		t.Fatalf("poke frame type = %d, want JSON", f.Type)
	}
	env, ok := parseEnvelope(f.Payload)
	if !ok || env.Type != "request_sd_health" {
		t.Fatalf("poke = %+v, want request_sd_health", env)
	}
}

// TestEventForward: an event message carries the mapped standard event code.
func TestEventForward(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	conn, _ := dialAndWelcome(t, deps)

	conn.Write(buildCommand("event", map[string]any{
		"name": "harsh_braking", "latitude": -29.8, "longitude": 30.9, "utc": 1750000001,
	}))

	select {
	case msg := <-received:
		events, _ := msg["events"].([]any)
		if len(events) == 0 {
			t.Fatalf("expected an event, got none: %v", msg["events"])
		}
		first, _ := events[0].([]any)
		if len(first) == 0 || first[0] != "HARSH:BRAKING" {
			t.Fatalf("event = %v, want HARSH:BRAKING", events)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event webhook")
	}
}
