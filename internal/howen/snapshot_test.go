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

func TestParseSnapshotFiles(t *testing.T) {
	files := []map[string]any{
		{"chl": "1", "fn": "/mnt/sd1/picture/A.jpg", "fs": "102400", "st": "2026-06-26 10:00:00"},
		{"chl": "3", "fn": "/mnt/sd1/picture/B.jpg", "fs": "98000", "st": "2026-06-26 11:30:00"},
		{"chl": "2", "fn": "", "fs": "0", "st": ""}, // empty path dropped
	}
	got := parseSnapshotFiles(files, "general", 0)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].Channel != 0 || got[0].DevicePath != "/mnt/sd1/picture/A.jpg" || got[0].Size != 102400 {
		t.Errorf("file0 = %+v", got[0])
	}
	if got[0].Kind != "general" || got[0].UTC == 0 || got[0].DeviceTime != "2026-06-26 10:00:00" {
		t.Errorf("file0 meta = %+v", got[0])
	}
	if got[1].Channel != 2 { // chl 3 -> camera 2
		t.Errorf("file1 channel = %d", got[1].Channel)
	}
}

// TestEndToEndSnapshotSearch drives a saved-snapshot search: the session sends a
// file query (0x4060, ft=3) and the fake device replies with two file entries
// (err=8) terminated by err=9.
func TestEndToEndSnapshotSearch(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		r.read(t)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := hub.Get(serial); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, ok := hub.Snapshotter(serial)
	if !ok {
		t.Fatal("no Snapshotter")
	}

	type result struct {
		files []gateway.SnapshotFileInfo
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		files, err := snap.SearchSnapshots(cctx, -1, 1750000000, 1750100000, "general")
		resCh <- result{files, err}
	}()

	fq := r.read(t)
	if fq.Type != msgFileQuery {
		t.Fatalf("expected 0x4060, got 0x%x", fq.Type)
	}
	obj, _ := parseHowenJSONObject(fq.Payload)
	if toString(obj["ft"]) != "3" {
		t.Fatalf("ft = %q, want 3 (general snapshot)", toString(obj["ft"]))
	}
	ss := toString(obj["ss"])
	conn.Write(buildHowenJSONFrame(msgFileQueryResponse, map[string]any{
		"ss": ss, "err": "8",
		"fi": map[string]any{"chl": "1", "fn": "/mnt/sd1/picture/A.jpg", "fs": "102400", "st": "2026-06-26 10:00:00"},
	}))
	conn.Write(buildHowenJSONFrame(msgFileQueryResponse, map[string]any{"ss": ss, "err": "9"}))

	select {
	case got := <-resCh:
		if got.err != nil {
			t.Fatalf("search failed: %v", got.err)
		}
		if len(got.files) != 1 || got.files[0].DevicePath != "/mnt/sd1/picture/A.jpg" {
			t.Fatalf("files = %+v", got.files)
		}
		if got.files[0].Kind != "general" {
			t.Errorf("kind = %q", got.files[0].Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search did not resolve")
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
