package howen

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/media"
	"github.com/dfm/device-gateway/internal/core/message"
)

// mediaDataFrame builds a 0x0011 media frame carrying raw file bytes (header
// fields are irrelevant to the file-transfer path).
func mediaDataFrame(data []byte) []byte {
	p := make([]byte, 12+len(data))
	binary.LittleEndian.PutUint16(p[0:2], 5) // media type: file
	binary.LittleEndian.PutUint16(p[2:4], 1) // channel
	copy(p[12:], data)
	return buildHowenFrame(msgMediaData, p)
}

// TestEndToEndSnapshotImage drives the full Phase-2 path: capture (0x4020/0x1020)
// then file-transfer (0x4090/0x1090) with the device streaming the JPEG bytes to
// the media port, and asserts CaptureImage returns those bytes.
func TestEndToEndSnapshotImage(t *testing.T) {
	snaps := media.NewSnapshotFetch()
	mgr := media.NewManager(t.TempDir(), "ffmpeg", logging.New("test"))
	hub := gateway.NewHub()
	deps := gateway.Deps{
		Config:             config.Config{ListenPort: 33000},
		Log:                logging.New("test"),
		Builder:            message.NewBuilder("test-gw", 0),
		Auth:               device.AllowAll{},
		Hub:                hub,
		Media:              mgr,
		Snapshots:          snaps,
		MediaAdvertiseHost: "127.0.0.1:1", // embedded in the 0x4090 srv; device ignores it here
	}
	srv := gateway.New(New(), deps)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	// Stand up the media server on a reserved port, sharing the snaps registry.
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mediaAddr := tmp.Addr().String()
	tmp.Close()
	ms := New().NewMediaServer(mediaAddr, mgr, nil, snaps, logging.New("test"))
	go ms.ListenAndServe(ctx)

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
			t.Fatal("device never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	snap, ok := hub.Snapshotter(serial)
	if !ok {
		t.Fatal("no Snapshotter")
	}

	jpeg := []byte("\xff\xd8\xff\xe0JFIF-fake-jpeg-bytes\xff\xd9")
	type result struct {
		img []byte
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		cctx, c := context.WithTimeout(context.Background(), 8*time.Second)
		defer c()
		img, err := snap.CaptureImage(cctx, 0, 0)
		resCh <- result{img, err}
	}()

	// 1) Capture request 0x4020 -> reply 0x1020 with a file path.
	cap := r.read(t)
	if cap.Type != msgSnapshot {
		t.Fatalf("expected 0x4020, got 0x%x", cap.Type)
	}
	capObj, _ := parseHowenJSONObject(cap.Payload)
	conn.Write(buildHowenJSONFrame(msgSnapshotResponse, map[string]any{
		"ss":  toString(capObj["ss"]),
		"err": "0",
		"rl":  []any{map[string]any{"ch": "1", "fn": "/mnt/sd1/picture/Pic.jpg"}},
	}))

	// 2) File-transfer request 0x4090 -> reply 0x1090, then deliver the bytes.
	ft := r.read(t)
	if ft.Type != msgFileTransfer {
		t.Fatalf("expected 0x4090, got 0x%x", ft.Type)
	}
	ftObj, _ := parseHowenJSONObject(ft.Payload)
	ftSS := toString(ftObj["ss"])
	if ftSS == "" {
		t.Fatal("file-transfer frame missing ss")
	}
	if toString(ftObj["fn"]) != "/mnt/sd1/picture/Pic.jpg" {
		t.Fatalf("fn = %q", ftObj["fn"])
	}
	conn.Write(buildHowenJSONFrame(msgFileTransferResponse, map[string]any{"ss": ftSS, "err": "0"}))

	// 3) Device dials the media port, registers with ftSS, streams the file.
	mconn, err := net.Dial("tcp", mediaAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer mconn.Close()
	mconn.Write(buildHowenJSONFrame(msgMediaRegister, map[string]any{"ss": ftSS}))
	mconn.Write(mediaDataFrame(jpeg[:10]))
	mconn.Write(mediaDataFrame(jpeg[10:]))
	mconn.Write(mediaDataFrame(nil)) // zero-length frame = end of file

	select {
	case got := <-resCh:
		if got.err != nil {
			t.Fatalf("CaptureImage failed: %v", got.err)
		}
		if string(got.img) != string(jpeg) {
			t.Fatalf("image mismatch: got %q", got.img)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CaptureImage did not resolve")
	}
}
