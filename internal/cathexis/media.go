package cathexis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/media"
)

// NewMediaServer implements gateway.MediaServerProvider: the app runner builds the
// Cathexis device-side media listener when video is enabled. Devices dial this port
// (a welcome identifies the serial) and then upload live H.264 (type 2) or a
// recorded MP4 (type 5).
func (*Protocol) NewMediaServer(addr string, mgr *media.Manager, clips *media.ClipRegistry, _ *media.SnapshotFetch, log *logging.Logger) gateway.MediaListener {
	// Cathexis has no file-transfer/snapshot path, so the SnapshotFetch registry is ignored.
	return &mediaServer{addr: addr, manager: mgr, clips: clips, log: log.With("tcp/cathexis-media")}
}

type mediaServer struct {
	addr    string
	manager *media.Manager
	clips   *media.ClipRegistry
	log     *logging.Logger
}

func (ms *mediaServer) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", ms.addr)
	if err != nil {
		return err
	}
	ms.log.Info(map[string]any{"event": "media_listening", "addr": ms.addr})
	go func() { <-ctx.Done(); ln.Close() }()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			ms.log.Debug(map[string]any{"event": "media_accept_error", "error": err.Error()})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.handle(conn)
		}()
	}
}

func (ms *mediaServer) handle(conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}
	r := bufio.NewReaderSize(conn, 64*1024)
	var serial string
	var clipSS string
	var clipDone bool

	finishOnExit := func() {
		// A clip connection that drops before the end marker yields an unplayable
		// partial — abort it rather than mark it ready.
		if clipSS != "" && !clipDone && ms.clips != nil {
			ms.clips.Abort(clipSS, "media connection closed before clip end")
		}
	}
	defer finishOnExit()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		var hdr [headerSize]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		h, err := readHeader(hdr[:])
		if err != nil {
			ms.log.Debug(map[string]any{"event": "media_bad_header", "error": err.Error()})
			return
		}
		if h.Size > maxFramePayld {
			ms.log.Debug(map[string]any{"event": "media_payload_too_large", "len": h.Size})
			return
		}
		payload := make([]byte, h.Size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}

		switch h.Type {
		case frameJSON:
			if env, ok := parseEnvelope(payload); ok && env.Type == "welcome" {
				serial = device.NormalizeSerial(toString(env.Payload["serial"]))
				ms.log.Info(map[string]any{"event": "media_welcome", "serial": serial, "remote": conn.RemoteAddr().String()})
			}

		case frameVideo:
			if serial == "" {
				continue
			}
			vf, ok := parseVideoFrame(payload)
			if !ok {
				continue
			}
			ss := liveSessionID(serial, vf.Camera, vf.Profile)
			if err := ms.manager.WriteVideo(ss, vf.Data); err != nil {
				// no active live stream for this ss — drop the connection
				ms.log.Debug(map[string]any{"event": "media_write_failed", "ss": ss, "error": err.Error()})
				return
			}

		case frameClip:
			if ms.clips == nil || serial == "" {
				continue
			}
			cc, ok := parseClipChunk(payload)
			if !ok {
				continue
			}
			ss := fmt.Sprintf("clip_%s_%d_%d_%d", serial, cc.Camera, cc.Profile, cc.StartUTC)
			clipSS = ss
			if len(cc.Data) > 0 {
				ms.clips.WriteRaw(ss, cc.Data)
			}
			if cc.EndChunk {
				ms.clips.FinishRaw(ss)
				clipDone = true
				clipSS = ""
			}

		default:
			// audio (3), event-preview (15), heartbeat (0): ignored in v1
		}
	}
}
