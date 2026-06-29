package navtelecom

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

const testIMEI = "863151075601887"

// readServerNTCB reads one NTCB packet (16-byte header + body) from the server
// and returns the body.
func readServerNTCB(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	head := make([]byte, ntcbHeaderLen)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("reading NTCB header: %v", err)
	}
	hdr, err := parseNTCBHeader(head)
	if err != nil {
		t.Fatalf("parsing NTCB header: %v", err)
	}
	body := make([]byte, hdr.BodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("reading NTCB body: %v", err)
	}
	return body
}

// TestEndToEndHandshakeAndTelemetry drives the full stack: identity handshake →
// FLEX negotiation → a `~A` telemetry array, asserting the device is ACKed at
// each step and a correct universal webhook message is produced.
func TestEndToEndHandshakeAndTelemetry(t *testing.T) {
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
		Config:  config.Config{ListenPort: 4000},
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

	// 1. Identity handshake: device → server `*>S:<imei>`, expect `*<S`.
	identity := append([]byte("*>S:"), []byte(testIMEI)...)
	conn.Write(buildNTCB(1, 0, identity))
	if body := readServerNTCB(t, conn); string(body) != "*<S" {
		t.Fatalf("identity reply = %q, want *<S", body)
	}

	// 2. FLEX negotiation (offer 1.0 so no renegotiation), expect `*<FLEX`.
	conn.Write(buildNTCB(1, 0, buildFlexNegotiationBody(flexVer10, flexVer10, 69, testFields...)))
	if body := readServerNTCB(t, conn); string(body[:6]) != "*<FLEX" {
		t.Fatalf("flex reply = %q, want *<FLEX…", body)
	}

	// Wait until the device is registered in the hub.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := deps.Hub.Get(testIMEI); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device never registered in hub")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 3. Telemetry: `~A` with one record. Expect a `~A<size><crc8>` ACK and a webhook.
	flexA := []byte{markerFLEX, flexArray, 1}
	flexA = append(flexA, encodeRecord()...)
	flexA = append(flexA, crc8(flexA))
	conn.Write(flexA)

	ack := make([]byte, 4) // ~A size crc8
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("reading ~A ack: %v", err)
	}
	if ack[0] != markerFLEX || ack[1] != flexArray || ack[2] != 1 || ack[3] != crc8(ack[:3]) {
		t.Fatalf("~A ack = % x, malformed", ack)
	}

	select {
	case msg := <-received:
		dev, _ := msg["device"].(map[string]any)
		if dev["imei"] != testIMEI {
			t.Fatalf("imei = %v, want %s", dev["imei"], testIMEI)
		}
		if dev["type"] != "navtelecom" {
			t.Fatalf("device.type = %v, want navtelecom", dev["type"])
		}
		if msg["valid"] != true {
			t.Fatalf("valid = %v, want true", msg["valid"])
		}
		gps, _ := msg["gps"].(map[string]any)
		lat, _ := gps["latitude"].(float64)
		if lat < 55.70 || lat > 55.71 {
			t.Fatalf("gps.latitude = %v, want ~55.704", gps["latitude"])
		}
		if sp, _ := gps["speed"].(float64); sp != 60.5 {
			t.Fatalf("gps.speed = %v, want 60.5", gps["speed"])
		}
		// The record's event id (100, non-0xFF00) is a real event; unmapped, it
		// passes through as "NTC:100".
		events, _ := msg["events"].([]any)
		if len(events) == 0 {
			t.Fatalf("expected an event, got none: %v", msg["events"])
		}
		first, _ := events[0].([]any)
		if len(first) == 0 || first[0] != "NTC:100" {
			t.Fatalf("event = %v, want NTC:100", events)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

// TestPingNoResponse confirms a 0x7F FLEX ping is accepted and not answered.
func TestPingNoResponse(t *testing.T) {
	deps := gateway.Deps{
		Config:  config.Config{ListenPort: 4000},
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

	conn.Write([]byte{markerPing})
	// No response is expected; a short read should time out cleanly.
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatalf("ping unexpectedly answered with %#x", buf[0])
	}
}
