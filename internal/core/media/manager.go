// Package media runs live HLS streams. A protocol plugin feeds raw H.264 frames
// for a stream id; the manager spawns one ffmpeg per stream that remuxes the
// H.264 (no transcode, -c:v copy) into an HLS playlist + segments on disk, which
// the HTTP API serves to a browser player.
package media

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// Manager owns the set of active live streams.
type Manager struct {
	hlsRoot  string
	ffmpeg   string
	segDur   int // HLS segment duration (seconds)
	listSize int // segments kept in the playlist
	log      *logging.Logger

	mu      sync.Mutex
	streams map[string]*Stream
}

// NewManager constructs a media manager writing HLS under hlsRoot.
func NewManager(hlsRoot, ffmpegPath string, log *logging.Logger) *Manager {
	return &Manager{
		hlsRoot:  hlsRoot,
		ffmpeg:   ffmpegPath,
		segDur:   2,
		listSize: 6,
		log:      log.With("media"),
		streams:  map[string]*Stream{},
	}
}

// HLSRoot is the directory HLS output is written under (served by the HTTP API).
func (m *Manager) HLSRoot() string { return m.hlsRoot }

// Stream is one ffmpeg process fed H.264/H.265: either a live stream producing
// rolling HLS (Dir set), or a recorded clip producing a single .mp4 (outFile
// set). Frames are written the same way; only the ffmpeg output differs.
type Stream struct {
	ID      string
	Serial  string
	Camera  int
	Profile int
	Dir     string // HLS output dir (<root>/<serial>/<camera>/<profile>); empty for clips
	outFile string // clip .mp4 path; empty for HLS
	codec   string // input codec for clips: "h264" or "hevc"
	Started time.Time

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{} // closed once ffmpeg has exited (clip finalize waits on it)
	running    bool          // ffmpeg spawned
	closed     bool
	bytes      int64
	lastWrite  time.Time // last time a frame was written (device liveness)
	lastAccess time.Time // last time a viewer fetched the playlist (viewer liveness)
}

// Register creates (or replaces) a stream entry and its output directory. ffmpeg
// is started lazily on the first video frame, so a stream the device never
// connects to costs nothing.
func (m *Manager) Register(id, serial string, camera, profile int) (*Stream, error) {
	dir := filepath.Join(m.hlsRoot, serial, strconv.Itoa(camera), strconv.Itoa(profile))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("media: mkdir %s: %w", dir, err)
	}
	// Clear stale segments/playlist from a previous run of this stream.
	clearDir(dir)

	m.mu.Lock()
	if old := m.streams[id]; old != nil {
		old.stop()
	}
	s := &Stream{ID: id, Serial: serial, Camera: camera, Profile: profile, Dir: dir, Started: time.Now()}
	m.streams[id] = s
	m.mu.Unlock()
	m.log.Debug(map[string]any{"event": "stream_registered", "id": id, "serial": serial, "camera": camera, "profile": profile})
	return s, nil
}

// RegisterClip creates (or replaces) a clip stream that remuxes incoming frames
// into a single .mp4 at outFile. ffmpeg starts lazily on the first frame, using
// the given input codec ("h264" or "hevc"). The parent directory is created.
func (m *Manager) RegisterClip(id, serial string, camera, profile int, outFile, codec string) (*Stream, error) {
	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		return nil, fmt.Errorf("media: mkdir %s: %w", filepath.Dir(outFile), err)
	}
	_ = os.Remove(outFile) // clear any stale partial from a previous attempt
	if codec != "hevc" {
		codec = "h264"
	}

	m.mu.Lock()
	if old := m.streams[id]; old != nil {
		old.stop()
	}
	s := &Stream{ID: id, Serial: serial, Camera: camera, Profile: profile, outFile: outFile, codec: codec, Started: time.Now()}
	m.streams[id] = s
	m.mu.Unlock()
	m.log.Debug(map[string]any{"event": "clip_registered", "id": id, "serial": serial, "out": outFile, "codec": codec})
	return s, nil
}

// FinishClip gracefully closes a clip's ffmpeg (so the .mp4 moov atom is written)
// and returns the final file size. Unlike Stop it does not delete the output.
func (m *Manager) FinishClip(id string) (int64, error) {
	m.mu.Lock()
	s := m.streams[id]
	delete(m.streams, id)
	m.mu.Unlock()
	if s == nil {
		return 0, fmt.Errorf("media: no clip %q", id)
	}

	s.mu.Lock()
	out := s.outFile
	if !s.closed {
		s.closed = true
		if s.stdin != nil {
			s.stdin.Close() // EOF → ffmpeg flushes and exits
		}
	}
	done := s.done
	s.mu.Unlock()

	// Wait for ffmpeg to fully exit so the .mp4 moov atom is written, but don't
	// hang forever if it wedges.
	if done != nil {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			s.stop() // kill a stuck ffmpeg
		}
	}
	if out == "" {
		return 0, nil
	}
	fi, err := os.Stat(out)
	if err != nil {
		return 0, err
	}
	m.log.Debug(map[string]any{"event": "clip_finished", "id": id, "out": out, "bytes": fi.Size()})
	return fi.Size(), nil
}

// Get returns a stream by id.
func (m *Manager) Get(id string) (*Stream, bool) {
	m.mu.Lock()
	s, ok := m.streams[id]
	m.mu.Unlock()
	return s, ok
}

// WriteVideo appends an H.264 access-unit to a stream's ffmpeg input, starting
// ffmpeg on the first frame. Returns an error if the stream is unknown/closed.
func (m *Manager) WriteVideo(id string, h264 []byte) error {
	s, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("media: no stream %q", id)
	}
	return s.write(m, h264)
}

// Stop terminates a stream's ffmpeg and removes its output.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	s := m.streams[id]
	delete(m.streams, id)
	m.mu.Unlock()
	if s != nil {
		s.stop()
		if s.outFile != "" {
			_ = os.Remove(s.outFile) // clip: drop the partial .mp4
		} else {
			clearDir(s.Dir) // live: drop HLS segments/playlist
		}
		m.log.Debug(map[string]any{"event": "stream_stopped", "id": id})
	}
}

// liveReapIdle is how long a live stream may look abandoned — no frames from the
// device AND no playlist fetch from a viewer — before the reaper stops it. It is
// generous relative to the media-connection idle timeout (60s) and a player's
// ~2s playlist poll, so a healthy stream is never reaped.
const liveReapIdle = 90 * time.Second

// liveReapInterval is how often the reaper scans for abandoned streams.
const liveReapInterval = 30 * time.Second

// StartReaper runs a background loop that stops abandoned LIVE streams until ctx
// is cancelled. A live stream leaks its ffmpeg two ways: the device's media
// connection drops (frames stop, ffmpeg blocks on stdin forever), or the browser
// navigates away without calling stop (the device keeps streaming to nobody).
// The reaper catches both — it stops a live stream once both its last frame and
// its last playlist fetch are older than liveReapIdle. Clips are never touched
// here; they finalize on PLAYBACK_END or media-connection close.
func (m *Manager) StartReaper(ctx context.Context) {
	go func() {
		t := time.NewTicker(liveReapInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.reapIdle(time.Now(), liveReapIdle)
			}
		}
	}()
}

// reapIdle stops every live stream idle for longer than idle as of now.
func (m *Manager) reapIdle(now time.Time, idle time.Duration) {
	var dead []string
	m.mu.Lock()
	for id, s := range m.streams {
		if s.outFile != "" {
			continue // a clip — finalized by the clip pipeline, not reaped here
		}
		if s.idleFor(now) > idle {
			dead = append(dead, id)
		}
	}
	m.mu.Unlock()
	for _, id := range dead {
		m.log.Info(map[string]any{"event": "stream_reaped", "id": id, "reason": "idle"})
		m.Stop(id)
	}
}

// idleFor reports how long a live stream has looked abandoned: the longer of
// "no frame written" and "no playlist fetched", each measured from the stream's
// start until that event first happens. A healthy live view both receives frames
// and is polled by a player, so its idle stays near zero; if either signal goes
// quiet the idle climbs and the stream is reaped.
func (s *Stream) idleFor(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	frameRef := s.lastWrite
	if frameRef.IsZero() {
		frameRef = s.Started
	}
	accessRef := s.lastAccess
	if accessRef.IsZero() {
		accessRef = s.Started
	}
	idle := now.Sub(frameRef)
	if a := now.Sub(accessRef); a > idle {
		idle = a
	}
	return idle
}

// TouchPlaylistPath marks the live stream whose HLS output is relPath (e.g.
// "<serial>/<camera>/<profile>/stream.m3u8", relative to the HLS root) as still
// being watched, resetting its viewer-idle clock. Called when the HTTP API
// serves a playlist. Unknown paths (e.g. another unit's stream) are ignored.
func (m *Manager) TouchPlaylistPath(relPath string) {
	dir := filepath.Dir(filepath.Join(m.hlsRoot, relPath))
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.streams {
		if s.outFile == "" && s.Dir == dir {
			s.mu.Lock()
			s.lastAccess = now
			s.mu.Unlock()
			return
		}
	}
}

// Status reports a stream's liveness for the API.
func (m *Manager) Status(id string) (map[string]any, bool) {
	s, ok := m.Get(id)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"id": s.ID, "serial": s.Serial, "camera": s.Camera, "profile": s.Profile,
		"active": s.running && !s.closed, "bytes": s.bytes,
		"uptime_ms": time.Since(s.Started).Milliseconds(),
	}, true
}

func (s *Stream) write(m *Manager, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("media: stream %q closed", s.ID)
	}
	if !s.running {
		if err := s.spawn(m); err != nil {
			return err
		}
		s.running = true
	}
	s.lastWrite = time.Now()
	n, err := s.stdin.Write(data)
	s.bytes += int64(n)
	return err
}

// spawn starts ffmpeg: read Annex-B H.264 from stdin, copy the video into a
// rolling HLS playlist (no re-encode).
func (s *Stream) spawn(m *Manager) error {
	var args []string
	if s.outFile != "" {
		// Clip: remux the recorded elementary stream into a single .mp4 (no
		// re-encode). +faststart moves the moov atom up so the file is seekable.
		codec := s.codec
		if codec == "" {
			codec = "h264"
		}
		args = []string{
			"-hide_banner", "-loglevel", "error",
			"-fflags", "+genpts",
			"-f", codec, "-i", "pipe:0",
			"-an", "-c", "copy",
			"-movflags", "+faststart",
			"-y", s.outFile,
		}
	} else {
		playlist := filepath.Join(s.Dir, "stream.m3u8")
		segments := filepath.Join(s.Dir, "seg_%03d.ts")
		args = []string{
			"-hide_banner", "-loglevel", "error",
			"-fflags", "+genpts",
			"-f", "h264", "-i", "pipe:0",
			"-an", "-c:v", "copy",
			"-f", "hls",
			"-hls_time", strconv.Itoa(m.segDur),
			"-hls_list_size", strconv.Itoa(m.listSize),
			"-hls_flags", "delete_segments+independent_segments",
			"-hls_segment_filename", segments,
			playlist,
		}
	}
	cmd := exec.Command(m.ffmpeg, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("media: ffmpeg stdin: %w", err)
	}
	cmd.Stderr = &lineLogger{log: m.log, id: s.ID}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("media: ffmpeg start (is %q installed?): %w", m.ffmpeg, err)
	}
	s.cmd = cmd
	s.stdin = stdin
	s.done = make(chan struct{})
	m.log.Info(map[string]any{"event": "ffmpeg_started", "id": s.ID, "pid": cmd.Process.Pid, "dir": s.Dir, "out": s.outFile})
	done := s.done
	go func() {
		err := cmd.Wait()
		close(done)
		m.log.Debug(map[string]any{"event": "ffmpeg_exited", "id": s.ID, "error": errStr(err)})
	}()
	return nil
}

func (s *Stream) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// clearDir removes HLS artifacts (*.ts, *.m3u8) from a stream directory.
func clearDir(dir string) {
	for _, pat := range []string{"*.ts", "*.m3u8"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, f := range matches {
			_ = os.Remove(f)
		}
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// lineLogger forwards ffmpeg stderr to the structured log at debug level.
type lineLogger struct {
	log *logging.Logger
	id  string
}

func (w *lineLogger) Write(p []byte) (int, error) {
	w.log.Debug(map[string]any{"event": "ffmpeg", "id": w.id, "msg": string(p)})
	return len(p), nil
}
