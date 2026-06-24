package howen

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/media"
)

// NewMediaServer implements gateway.MediaServerProvider: the app runner calls it
// (only when video is enabled) to build the device-side media listener. *MediaServer
// satisfies gateway.MediaListener via its ListenAndServe method.
func (*Protocol) NewMediaServer(addr string, mgr *media.Manager, clips *media.ClipRegistry, log *logging.Logger) gateway.MediaListener {
	return &MediaServer{Addr: addr, Manager: mgr, Clips: clips, Log: log}
}

// MediaServer accepts the Howen media (video) connections the device opens after
// a live-preview command. It is a separate TCP listener from the control server:
// the device registers a media session (0x1002) — keyed by the same session id
// the control side sent — then streams 0x0011 frames, which are routed to the
// matching live stream's ffmpeg.
type MediaServer struct {
	Addr        string              // host:port to bind (e.g. 0.0.0.0:33001)
	Manager     *media.Manager      // where live video frames are written (HLS)
	Clips       *media.ClipRegistry // where recorded-clip frames are written (.mp4); may be nil
	Log         *logging.Logger
	idleTimeout time.Duration
}

// ListenAndServe serves the media port until ctx is cancelled.
func (ms *MediaServer) ListenAndServe(ctx context.Context) error {
	if ms.idleTimeout == 0 {
		ms.idleTimeout = 60 * time.Second
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", ms.Addr)
	if err != nil {
		return err
	}
	ms.Log.Info(map[string]any{"event": "media_listening", "addr": ms.Addr})
	go func() { <-ctx.Done(); ln.Close() }()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			ms.Log.Debug(map[string]any{"event": "media_accept_error", "error": err.Error()})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.handle(conn)
		}()
	}
}

func (ms *MediaServer) handle(conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}
	r := bufio.NewReaderSize(conn, 64*1024)
	var sessionID string
	var isClip bool
	var frames int

	for {
		_ = conn.SetReadDeadline(time.Now().Add(ms.idleTimeout))
		var header [howenHeaderSize]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if sessionID != "" {
				ms.Log.Debug(map[string]any{"event": "media_closed", "ss": sessionID, "frames": frames, "error": ioErr(err)})
			}
			// A clip's media connection closing (EOF/idle) is a finalize signal —
			// the device may upload the whole clip then disconnect without a
			// separate PLAYBACK_END. Finish is idempotent with the control-side end.
			if isClip && ms.Clips != nil {
				ms.Clips.Finish(sessionID)
			}
			return
		}
		h, err := readHowenFrameHeader(header[:])
		if err != nil {
			ms.Log.Debug(map[string]any{"event": "media_bad_header", "error": err.Error()})
			return
		}
		if h.PayloadLength > maxMediaPayloadBytes {
			ms.Log.Debug(map[string]any{"event": "media_payload_too_large", "len": h.PayloadLength})
			return
		}
		payload := make([]byte, h.PayloadLength)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}

		switch h.Type {
		case msgMediaRegister: // 0x1002 — bind this connection to a live stream or clip
			obj, _ := parseHowenJSONObject(payload)
			sessionID = toString(obj["ss"])
			isClip = ms.Clips != nil && ms.Clips.IsClip(sessionID)
			_, _ = conn.Write(buildHowenJSONFrame(msgMediaRegisterResponse, map[string]any{"ss": sessionID, "err": "0"}))
			ms.Log.Info(map[string]any{"event": "media_register", "ss": sessionID, "clip": isClip, "remote": conn.RemoteAddr().String()})

		case msgMediaData: // 0x0011 — a media frame
			if sessionID == "" {
				continue
			}
			mf, ok := parseHowenMediaFrame(payload)
			if !ok || !mf.isVideo() {
				continue // skip audio / malformed
			}
			frames++
			if isClip {
				// Recorded clip → remux to .mp4 (gated to the first keyframe).
				ms.Clips.WriteFrame(sessionID, mf.MediaType == 1, mf.Data)
				continue
			}
			if err := ms.Manager.WriteVideo(sessionID, mf.Data); err != nil {
				// The live stream was stopped/unknown — drop the connection.
				ms.Log.Debug(map[string]any{"event": "media_write_failed", "ss": sessionID, "error": err.Error()})
				return
			}
		}
	}
}

// maxMediaPayloadBytes bounds a single media frame (keyframes can be large).
const maxMediaPayloadBytes = 8 * 1024 * 1024

func ioErr(err error) string {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "eof"
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "idle_timeout"
	}
	return err.Error()
}
