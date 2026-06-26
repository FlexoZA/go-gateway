package howen

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/message"
)

func TestParseSnapshotResult(t *testing.T) {
	// Shape from H-Protocol §2.5.2.
	resp := map[string]any{
		"ss":  "snap_1",
		"err": "0",
		"rl": []any{
			map[string]any{"ch": "1", "fn": "/mnt/sd1/picture/Pic_a.jpg"},
			map[string]any{"ch": "4", "fn": "/mnt/sd1/picture/Pic_b.jpg"},
			map[string]any{"ch": "9", "fn": ""}, // empty path skipped
		},
	}
	files := parseSnapshotResult(resp)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Channel != 1 || files[0].DevicePath != "/mnt/sd1/picture/Pic_a.jpg" {
		t.Errorf("file0 = %+v", files[0])
	}
	if files[1].Channel != 4 {
		t.Errorf("file1 ch = %d", files[1].Channel)
	}
	if got := parseSnapshotResult(map[string]any{"err": "0"}); got != nil {
		t.Errorf("missing rl should yield nil, got %v", got)
	}
}

// TestEndToEndSnapshot drives the full snapshot path: register a device, trigger
// a snapshot from the "HTTP side" via the hub, assert the 0x4020 request frame,
// reply with a 0x1020 response, and confirm the parsed file paths come back.
func TestEndToEndSnapshot(t *testing.T) {
	hub := gateway.NewHub()
	deps := gateway.Deps{
		Config:  config.Config{ListenPort: 33000},
		Log:     logging.New("test"),
		Builder: message.NewBuilder("test-gw", 0),
		Auth:    device.AllowAll{},
		Hub:     hub,
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

	const serial = "864312087845313"
	conn.Write(buildHowenJSONFrame(msgSignalRegister, map[string]any{"dn": serial, "fw": "Hero-MC30-02"}))
	r := newConnReader(conn)
	for i := 0; i < 3; i++ { // register response + gps subscribe + alarm subscribe
		r.read(t)
	}

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

	snap, ok := hub.Snapshotter(serial)
	if !ok {
		t.Fatal("session does not expose Snapshotter")
	}

	type result struct {
		res gateway.SnapshotResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		res, err := snap.RequestSnapshot(cctx, []int{0, 3}, 2)
		resCh <- result{res, err}
	}()

	frame := r.read(t)
	if frame.Type != msgSnapshot {
		t.Fatalf("expected SNAPSHOT (0x%x), got 0x%x", msgSnapshot, frame.Type)
	}
	obj, _ := parseHowenJSONObject(frame.Payload)
	ss := toString(obj["ss"])
	if ss == "" {
		t.Fatal("snapshot frame missing session id")
	}
	if cl := toString(obj["cl"]); cl != "1;4" { // 0-based cameras 0,3 -> channels 1,4
		t.Fatalf("cl = %q, want 1;4", cl)
	}
	if res := toString(obj["res"]); res != "2" {
		t.Fatalf("res = %q, want 2", res)
	}

	conn.Write(buildHowenJSONFrame(msgSnapshotResponse, map[string]any{
		"ss":  ss,
		"err": "0",
		"rl": []any{
			map[string]any{"ch": "1", "fn": "/mnt/sd1/picture/Pic_ch1.jpg"},
			map[string]any{"ch": "4", "fn": "/mnt/sd1/picture/Pic_ch4.jpg"},
		},
	}))

	select {
	case got := <-resCh:
		if got.err != nil {
			t.Fatalf("snapshot failed: %v", got.err)
		}
		if len(got.res.Files) != 2 {
			t.Fatalf("got %d files, want 2", len(got.res.Files))
		}
		if got.res.Files[0].DevicePath != "/mnt/sd1/picture/Pic_ch1.jpg" {
			t.Errorf("file0 = %+v", got.res.Files[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot did not resolve")
	}
}
