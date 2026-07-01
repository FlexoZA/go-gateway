package jt808

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

const testPhone = "96750"

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
		Config:  config.Config{ListenPort: N62().ControlPort},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
		Sinks:   []gateway.Sink{webhook.New(ts.URL)},
		Auth:    device.AllowAll{},
		Hub:     gateway.NewHub(),
	}
}

func startServer(t *testing.T, deps gateway.Deps) net.Conn {
	t.Helper()
	srv := gateway.New(New(N62()), deps)
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
	return conn
}

// TestRegisterAndLocation: a 0x0100 register registers the device in the hub and
// gets a 0x8100 reply, then a 0x0200 location is forwarded as a universal message
// with the jt808_19 device type and N62 model.
func TestRegisterAndLocation(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	conn := startServer(t, deps)

	// Register.
	if _, err := conn.Write(buildFrame(msgRegister, testPhone, 1, []byte("regbody"))); err != nil {
		t.Fatal(err)
	}
	const serial = "JT808_96750"
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

	// Drain the device's reply (0x8100 registration response + 0x8001 ack).
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 256)
	if n, err := conn.Read(buf); err != nil || n == 0 || buf[0] != flag {
		t.Fatalf("expected a framed reply, got n=%d err=%v", n, err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	// Location: Gauteng fix.
	when := time.Date(2026, 1, 27, 11, 29, 26, 0, time.UTC)
	body := buildLocationBody(0, 1<<1|1<<2, 26084200, 27937600, 1400, 605, 218, when,
		tlv(0x31, 9))
	if _, err := conn.Write(buildFrame(msgLocation, testPhone, 2, body)); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-received:
		dev, _ := msg["device"].(map[string]any)
		if dev["serial_no"] != serial {
			t.Fatalf("serial_no = %v, want %s", dev["serial_no"], serial)
		}
		if dev["type"] != "n62" || dev["model"] != "n62" {
			t.Fatalf("device type/model = %v/%v, want n62/n62 (jt808Switch)", dev["type"], dev["model"])
		}
		gps, _ := msg["gps"].(map[string]any)
		if lat, _ := gps["latitude"].(float64); lat < -26.085 || lat > -26.083 {
			t.Fatalf("latitude = %v, want ~-26.0842", gps["latitude"])
		}
		if lon, _ := gps["longitude"].(float64); lon < 27.936 || lon > 27.939 {
			t.Fatalf("longitude = %v, want ~27.9376", gps["longitude"])
		}
		if sp, _ := gps["speed"].(float64); sp != 60.5 {
			t.Fatalf("speed = %v, want 60.5", gps["speed"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

// TestTzOffsetUnitSetting: the editable per-unit timezone setting overrides the
// global env offset, and the global is the fallback when no setting is present.
func TestTzOffsetUnitSetting(t *testing.T) {
	settings := gateway.NewUnitSettings()
	settings.Replace(map[string]string{settingTimezoneOffset: "2"})
	withSetting := &session{conn: &gateway.Conn{Deps: gateway.Deps{
		Config:       config.Config{DeviceTZOffsetHours: 0},
		UnitSettings: settings,
	}}}
	if got := withSetting.tzOffset(); got != 2 {
		t.Fatalf("tzOffset = %v, want 2 (unit setting overrides global)", got)
	}
	fallback := &session{conn: &gateway.Conn{Deps: gateway.Deps{
		Config: config.Config{DeviceTZOffsetHours: 5},
	}}}
	if got := fallback.tzOffset(); got != 5 {
		t.Fatalf("tzOffset = %v, want 5 (global fallback)", got)
	}
}

// TestSettingsSchema: the unit exposes its editable timezone setting.
func TestSettingsSchema(t *testing.T) {
	fields := (&Protocol{}).SettingsSchema()
	if len(fields) != 1 || fields[0].Key != settingTimezoneOffset || fields[0].Type != "number" {
		t.Fatalf("settings schema = %+v", fields)
	}
}

// TestEventLocationForwarded: a location with the collision alarm bit set is
// forwarded carrying the COLLISION event code.
func TestEventLocationForwarded(t *testing.T) {
	received := make(chan map[string]any, 4)
	deps := newDeps(t, received)
	conn := startServer(t, deps)

	if _, err := conn.Write(buildFrame(msgRegister, testPhone, 1, nil)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := deps.Hub.Get("JT808_96750"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	body := buildLocationBody(1<<29, 1<<1, 26084200, 27937600, 0, 0, 0, time.Now())
	if _, err := conn.Write(buildFrame(msgLocation, testPhone, 2, body)); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-received:
		events, _ := msg["events"].([]any)
		var found bool
		for _, e := range events {
			// Each event is encoded as a [name, ...detail] array.
			if pair, ok := e.([]any); ok && len(pair) > 0 {
				if pair[0] == "COLLISION" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("COLLISION not in events: %#v", events)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}
