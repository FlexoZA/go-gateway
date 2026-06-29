package cathexis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
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
	// The old gateway split media across two ports (clip receiver + live stream);
	// this unit handles both frame types from one handler, so it listens on the
	// advertised media port and the next one to accept a device targeting either.
	addrs := mediaAddrs(ms.addr)
	lc := net.ListenConfig{}
	var wg sync.WaitGroup
	listeners := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		ln, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			// Roll back any listeners already opened so the unit fails cleanly.
			for _, l := range listeners {
				l.Close()
			}
			return err
		}
		listeners = append(listeners, ln)
		ms.log.Info(map[string]any{"event": "media_listening", "addr": addr})
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			ms.acceptLoop(ctx, ln)
		}(ln)
	}
	go func() {
		<-ctx.Done()
		for _, ln := range listeners {
			ln.Close()
		}
	}()
	wg.Wait()
	return nil
}

// mediaAddrs returns the addresses the media server listens on: the advertised
// media port plus the next port (the old gateway's separate live-stream port).
func mediaAddrs(addr string) []string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{addr}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return []string{addr}
	}
	return []string{addr, net.JoinHostPort(host, strconv.Itoa(port+1))}
}

func (ms *mediaServer) acceptLoop(ctx context.Context, ln net.Listener) {
	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return
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
	// currentSS is the media-manager stream this connection feeds. It comes from the
	// welcome client_id (review) or, for a plain live connection, the first video
	// frame's camera/profile. Audio frames carry no camera/profile, so they route to
	// whatever currentSS the video already established on this connection.
	var currentSS string
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
			env, ok := parseEnvelope(payload)
			if !ok {
				continue
			}
			switch env.Type {
			case "welcome":
				serial = device.NormalizeSerial(toString(env.Payload["serial"]))
				if cid := toString(env.Payload["client_id"]); cid != "" {
					currentSS = cid // review (and any client_id'd stream) names its target up front
				}
				ms.log.Info(map[string]any{"event": "media_welcome", "serial": serial, "client_id": currentSS, "remote": conn.RemoteAddr().String()})
			case "status", "error":
				// Review/stream errors arrive on the media channel as a status/error
				// JSON ({category, text/message}); surface them for diagnostics.
				ms.log.Debug(map[string]any{"event": "media_status", "serial": serial, "category": toString(env.Payload["category"]), "text": toString(env.Payload["text"]) + toString(env.Payload["message"])})
			}

		case frameVideo:
			if serial == "" {
				continue
			}
			vf, ok := parseVideoFrame(payload)
			if !ok {
				continue
			}
			// A live connection has no client_id, so derive the target from the
			// first video frame's camera/profile and keep it for the connection.
			if currentSS == "" {
				currentSS = liveSessionID(serial, vf.Camera, vf.Profile)
			}
			if len(vf.Data) == 0 {
				// A zero-length video frame is the review end-of-stream marker (NULL
				// frame); nothing to write.
				continue
			}
			if err := ms.manager.WriteVideo(currentSS, vf.Data); err != nil {
				// no active live stream for this ss — drop the connection
				ms.log.Debug(map[string]any{"event": "media_write_failed", "ss": currentSS, "error": err.Error()})
				return
			}

		case frameAudio:
			// Audio carries no camera/profile; it belongs to whatever stream this
			// connection's video established. Drop until then, and ignore if the
			// stream has no audio track (WriteAudio is a no-op there).
			if currentSS == "" {
				continue
			}
			aac, ok := parseAudioFrame(payload)
			if !ok || len(aac) == 0 {
				continue
			}
			if err := ms.manager.WriteAudio(currentSS, aac); err != nil {
				ms.log.Debug(map[string]any{"event": "media_audio_write_failed", "ss": currentSS, "error": err.Error()})
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
			// event-preview (15) is control-channel; heartbeat (0) is a no-op here.
		}
	}
}
