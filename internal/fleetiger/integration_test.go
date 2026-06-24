package fleetiger

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

// TestEndToEndLocationForward drives the full stack: a device logs in (and is
// ACKed), then sends a real location packet that the gateway forwards as a correct
// universal webhook message.
func TestEndToEndLocationForward(t *testing.T) {
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
		Config:  config.Config{ListenPort: 8050},
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

	// Login (spec packet, IMEI 123456789012345); expect the 10-byte login ACK.
	login := hexBytes(t, "78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A")
	if _, err := conn.Write(login); err != nil {
		t.Fatal(err)
	}
	ack := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("reading login ack: %v", err)
	}
	if want := buildResponse(protoLogin, 0x0001); string(ack) != string(want) {
		t.Fatalf("login ack = % X, want % X", ack, want)
	}

	// Wait until the device is registered in the hub.
	const serial = "123456789012345"
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

	// Location packet (spec example) -> expect a universal webhook message.
	location := hexBytes(t, "78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F "+
		"01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A")
	if _, err := conn.Write(location); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-received:
		dev, _ := msg["device"].(map[string]any)
		if dev["serial_no"] != serial {
			t.Fatalf("serial_no = %v, want %s", dev["serial_no"], serial)
		}
		if dev["imei"] != serial {
			t.Fatalf("imei = %v, want %s", dev["imei"], serial)
		}
		if dev["type"] != "fleetiger" {
			t.Fatalf("device.type = %v, want fleetiger", dev["type"])
		}
		gps, _ := msg["gps"].(map[string]any)
		lat, _ := gps["latitude"].(float64)
		if lat < 23.1 || lat > 23.2 {
			t.Fatalf("gps.latitude = %v, want ~23.11", gps["latitude"])
		}
		if msg["valid"] != true {
			t.Fatalf("valid = %v, want true (positioning bit set)", msg["valid"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

// TestUnitSettingsTimezone confirms the session reads the timezone offset from the
// editable unit settings (not just the env config): with a +2h offset, the
// forwarded GPS timestamp is shifted 2h earlier than the UTC-decoded value.
func TestUnitSettingsTimezone(t *testing.T) {
	received := make(chan map[string]any, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		received <- msg
		w.WriteHeader(200)
	}))
	defer ts.Close()

	settings := gateway.NewUnitSettings()
	settings.Replace(map[string]string{settingTimezoneOffset: "2"})

	deps := gateway.Deps{
		Config:       config.Config{ListenPort: 8050},
		Log:          logging.New("test"),
		Builder:      message.NewBuilder("test-gw", 0),
		Sinks:        []gateway.Sink{webhook.New(ts.URL)},
		Auth:         device.AllowAll{},
		Hub:          gateway.NewHub(),
		UnitSettings: settings,
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

	conn.Write(hexBytes(t, "78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A"))
	ack := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	io.ReadFull(conn, ack)

	location := hexBytes(t, "78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F "+
		"01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A")
	conn.Write(location)

	// Expected UTC epoch when decoded with the +2h offset.
	withTz, _ := parseGt06Packet(location, 2)
	wantTS := time.Unix(withTz.GPS.UTC, 0).UTC().Format("2006-01-02T15:04:05") + "+00:00"

	select {
	case msg := <-received:
		gps, _ := msg["gps"].(map[string]any)
		if gps["timestamp"] != wantTS {
			t.Fatalf("gps.timestamp = %v, want %s (tz from unit settings)", gps["timestamp"], wantTS)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

// TestIgnitionEvents confirms an ACC transition in the heartbeat status emits an
// IGNITION:ON event, while the first (baseline) reading emits nothing.
func TestIgnitionEvents(t *testing.T) {
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
		Config:  config.Config{ListenPort: 8050},
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

	conn.Write(hexBytes(t, "78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A"))
	ack := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	io.ReadFull(conn, ack)

	// Baseline heartbeat: ACC off (terminalInfo bit1=0) → establishes state, no event.
	conn.Write(buildFrame(protoStatus, []byte{0x00, 0x06, 0x04, 0x00, 0x01}, 0x0001))
	// Transition heartbeat: ACC on (terminalInfo bit1=1) → IGNITION:ON.
	conn.Write(buildFrame(protoStatus, []byte{0x02, 0x06, 0x04, 0x00, 0x01}, 0x0002))

	select {
	case msg := <-received:
		events, _ := msg["events"].([]any)
		if len(events) == 0 {
			t.Fatalf("expected an ignition event, got none: %v", msg["events"])
		}
		first, _ := events[0].([]any)
		if len(first) == 0 || first[0] != "IGNITION:ON" {
			t.Fatalf("event = %v, want IGNITION:ON", events)
		}
		dev, _ := msg["device"].(map[string]any)
		if dev["serial_no"] != "123456789012345" {
			t.Fatalf("serial_no = %v", dev["serial_no"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ignition event")
	}
}

// TestHeartbeatAcked confirms the gateway answers a heartbeat so the device keeps
// the connection alive.
func TestHeartbeatAcked(t *testing.T) {
	deps := gateway.Deps{
		Config:  config.Config{ListenPort: 8050},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
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

	conn.Write(hexBytes(t, "78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A"))
	ack := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	io.ReadFull(conn, ack)

	// Heartbeat with serial 0x0007; expect a heartbeat ACK echoing it.
	conn.Write(buildFrame(protoStatus, []byte{0x44, 0x04, 0x03, 0x00, 0x01}, 0x0007))
	hbAck := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, hbAck); err != nil {
		t.Fatalf("reading heartbeat ack: %v", err)
	}
	if want := buildResponse(protoStatus, 0x0007); string(hbAck) != string(want) {
		t.Fatalf("heartbeat ack = % X, want % X", hbAck, want)
	}
}
