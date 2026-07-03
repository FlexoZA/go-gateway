package media

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(t.TempDir(), "ffmpeg", logging.New("test"))
}

func add(m *Manager, s *Stream) {
	m.mu.Lock()
	m.streams[s.ID] = s
	m.mu.Unlock()
}

func alive(m *Manager, id string) bool {
	_, ok := m.Get(id)
	return ok
}

// TestReapIdle covers the live-stream leak: a stream is reaped once both its
// device (frames) and its viewer (playlist) have gone quiet past the threshold,
// while a healthy stream and any clip survive.
func TestReapIdle(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	idle := 90 * time.Second
	old := now.Add(-2 * idle)
	dir := func(id string) string { return filepath.Join(m.hlsRoot, id) }

	live := func(id string, lastWrite, lastAccess time.Time) *Stream {
		return &Stream{ID: id, Dir: dir(id), Started: old, lastWrite: lastWrite, lastAccess: lastAccess}
	}

	healthy := live("healthy", now, now)
	deviceGone := live("device-gone", old, now)
	viewerGone := live("viewer-gone", now, old)
	neverConnected := &Stream{ID: "never", Dir: dir("never"), Started: old} // no frame, no fetch
	clip := &Stream{ID: "clip", outFile: filepath.Join(m.hlsRoot, "c.mp4"), Started: old, lastWrite: old}

	for _, s := range []*Stream{healthy, deviceGone, viewerGone, neverConnected, clip} {
		add(m, s)
	}

	m.reapIdle(now, idle)

	wantAlive := map[string]bool{
		"healthy": true,  // both signals fresh
		"clip":    true,  // clips are never reaped here
		"never":   false, // registered but abandoned before anything connected
	}
	wantReaped := []string{"device-gone", "viewer-gone", "never"}

	for id, want := range wantAlive {
		if got := alive(m, id); got != want {
			t.Errorf("after reap: stream %q alive=%v, want %v", id, got, want)
		}
	}
	for _, id := range wantReaped {
		if alive(m, id) {
			t.Errorf("after reap: stream %q should have been reaped", id)
		}
	}
}

// TestTouchPlaylistPathResetsViewerIdle verifies a playlist fetch keeps an
// otherwise viewer-idle (but device-active) stream alive.
func TestTouchPlaylistPathResetsViewerIdle(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	idle := 90 * time.Second
	old := now.Add(-2 * idle)

	serial, camera, profile := "SERIAL", "0", "1"
	streamDir := filepath.Join(m.hlsRoot, serial, camera, profile)
	s := &Stream{ID: "live_SERIAL", Dir: streamDir, Started: old, lastWrite: now, lastAccess: old}
	add(m, s)

	// Before the fetch: viewer has been idle past the threshold -> would be reaped.
	if got := s.idleFor(now); got <= idle {
		t.Fatalf("precondition: idleFor=%v, want > %v", got, idle)
	}

	m.TouchPlaylistPath(filepath.Join(serial, camera, profile, "stream.m3u8"))

	if got := s.idleFor(time.Now()); got > idle {
		t.Errorf("after TouchPlaylistPath: idleFor=%v, want <= %v (viewer marked active)", got, idle)
	}

	// A path for a stream we don't host must be a no-op, not a panic.
	m.TouchPlaylistPath("OTHER/0/0/stream.m3u8")
}

// TestRegistryLockReleasedBeforeStreamLock guards the deadlock the reaper exists to
// break: a write wedged on ffmpeg's stdin holds a stream's s.mu, and the reader
// paths (ActiveStreams/reapIdle/TouchPlaylistPath) must evaluate that stream's lock
// WITHOUT holding the registry lock m.mu — otherwise one stuck stream freezes the
// whole manager. We simulate the wedged write by holding a stream's s.mu, then
// assert m.mu is still acquirable while a reader is blocked on that stream.
func TestRegistryLockReleasedBeforeStreamLock(t *testing.T) {
	m := newTestManager(t)
	stuck := &Stream{ID: "stuck", Dir: filepath.Join(m.hlsRoot, "stuck"), Started: time.Now()}
	add(m, stuck)

	stuck.mu.Lock() // stand in for a write blocked on a non-draining ffmpeg stdin
	defer stuck.mu.Unlock()

	// ActiveStreams must touch every live stream, so it will block on stuck.s.mu —
	// but only after releasing m.mu.
	go m.ActiveStreams()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if m.mu.TryLock() {
			m.mu.Unlock()
			return // registry lock is free while the reader is blocked — correct
		}
		if time.Now().After(deadline) {
			t.Fatal("m.mu held while a reader was blocked on a stream lock — registry frozen")
		}
	}
}
