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
