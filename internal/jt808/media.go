package jt808

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/media"
)

// streamRoutes is shared state between the control sessions (which start live
// previews and request clips) and the media listener (which receives the device's
// JT1078 stream on a separate port and must route each connection to the right
// sink). Keyed by "<serial>|<channel>" (channel is 1-based). One instance lives on
// the *Protocol so both seams reach it.
type streamRoutes struct {
	mu       sync.Mutex
	live     map[string]liveRoute
	playback map[string]string // -> clip session id (ss)
}

type liveRoute struct {
	camera  int
	profile int
}

func newStreamRoutes() *streamRoutes {
	return &streamRoutes{live: map[string]liveRoute{}, playback: map[string]string{}}
}

func routeKey(serial string, channel int) string { return fmt.Sprintf("%s|%d", serial, channel) }

func (r *streamRoutes) setLive(serial string, channel, camera, profile int) {
	r.mu.Lock()
	r.live[routeKey(serial, channel)] = liveRoute{camera: camera, profile: profile}
	r.mu.Unlock()
}

func (r *streamRoutes) getLive(serial string, channel int) (liveRoute, bool) {
	r.mu.Lock()
	lr, ok := r.live[routeKey(serial, channel)]
	r.mu.Unlock()
	return lr, ok
}

func (r *streamRoutes) clearLive(serial string, channel int) {
	r.mu.Lock()
	delete(r.live, routeKey(serial, channel))
	r.mu.Unlock()
}

func (r *streamRoutes) setPlayback(serial string, channel int, ss string) {
	r.mu.Lock()
	r.playback[routeKey(serial, channel)] = ss
	r.mu.Unlock()
}

func (r *streamRoutes) getPlayback(serial string, channel int) (string, bool) {
	r.mu.Lock()
	ss, ok := r.playback[routeKey(serial, channel)]
	r.mu.Unlock()
	return ss, ok
}

func (r *streamRoutes) clearPlayback(serial string, channel int) {
	r.mu.Lock()
	delete(r.playback, routeKey(serial, channel))
	r.mu.Unlock()
}

// NewMediaServer implements gateway.MediaServerProvider: the app runner calls it
// (only when video is enabled) to build the device-side JT1078 media listener.
func (p *Protocol) NewMediaServer(addr string, mgr *media.Manager, clips *media.ClipRegistry, snaps *media.SnapshotFetch, log *logging.Logger) gateway.MediaListener {
	return &mediaServer{addr: addr, mgr: mgr, clips: clips, routes: p.routes, log: log.With(logNS + "-media")}
}

// mediaServer accepts the JT1078 stream connections the N62 opens after a
// 0x9101 (live) or 0x9201 (playback) command, parses the frames, and routes the
// H.264 to the live HLS manager or the recorded-clip registry.
type mediaServer struct {
	addr   string
	mgr    *media.Manager
	clips  *media.ClipRegistry
	routes *streamRoutes
	log    *logging.Logger

	idle time.Duration
}

func (ms *mediaServer) ListenAndServe(ctx context.Context) error {
	if ms.idle == 0 {
		ms.idle = 60 * time.Second
	}
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

	// Peek the first bytes for diagnostics: confirms the N62 uses the JT1078
	// 0x30316364 framing (and not the alternate RTP/length framing).
	if head, err := r.Peek(16); err == nil {
		ms.log.Info(map[string]any{"event": "media_connect", "remote": conn.RemoteAddr().String(), "head": hex.EncodeToString(head)})
	}

	var (
		serial   string
		channel  int
		decided  bool
		clipMode bool
		clipSS   string
		liveID   string
		liveChan int
		frames   int
	)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(ms.idle))
		f, err := readJT1078Frame(r)
		if err != nil {
			if serial != "" {
				ms.log.Debug(map[string]any{"event": "media_closed", "serial": serial, "frames": frames, "error": ioErr(err)})
			}
			if clipMode && ms.clips != nil {
				ms.clips.Finish(clipSS)
				ms.routes.clearPlayback(serial, liveChan)
			}
			return
		}
		if !f.isVideo() {
			continue // skip audio / transparent
		}
		serial = serialFromPhone(f.SimDigits)
		channel = f.Channel

		if !decided {
			decided = true
			liveChan = channel
			if ss, ok := ms.routes.getPlayback(serial, channel); ok {
				clipMode = true
				clipSS = ss
				ms.log.Info(map[string]any{"event": "media_playback", "serial": serial, "channel": channel, "ss": ss})
			} else {
				lr, ok := ms.routes.getLive(serial, channel)
				camera, profile := channel-1, 0
				if ok {
					camera, profile = lr.camera, lr.profile
				}
				liveID = liveSessionID(serial, camera, profile)
				ms.log.Info(map[string]any{"event": "media_live", "serial": serial, "channel": channel, "id": liveID})
			}
		}

		frames++
		if clipMode {
			if ms.clips != nil {
				ms.clips.WriteFrame(clipSS, f.isKeyframe(), f.Body)
			}
			continue
		}
		if err := ms.mgr.WriteVideo(liveID, f.Body); err != nil {
			// No live stream registered for this id (stopped/unknown) — drop the conn.
			ms.log.Debug(map[string]any{"event": "media_write_failed", "id": liveID, "error": err.Error()})
			return
		}
	}
}

// ioErr summarizes a read error for logging.
func ioErr(err error) string {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return "eof"
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "idle_timeout"
	}
	return err.Error()
}
