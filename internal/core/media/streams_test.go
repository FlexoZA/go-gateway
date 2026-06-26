package media

import (
	"path/filepath"
	"testing"
	"time"
)

// TestActiveStreamsAndStopAll verifies the dashboard backend: only live streams
// are counted/stopped, and clips are left alone.
func TestActiveStreamsAndStopAll(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	dir := func(id string) string { return filepath.Join(m.hlsRoot, id) }

	add(m, &Stream{ID: "live-a", Serial: "A", Camera: 0, Profile: 1, Dir: dir("a"), Started: now})
	add(m, &Stream{ID: "live-b", Serial: "B", Camera: 1, Profile: 0, Dir: dir("b"), Started: now})
	add(m, &Stream{ID: "clip-c", Serial: "C", outFile: filepath.Join(m.hlsRoot, "c.mp4"), Started: now})

	if got := len(m.ActiveStreams()); got != 2 {
		t.Fatalf("ActiveStreams = %d, want 2 (clip excluded)", got)
	}
	if n := m.StopAllLive(); n != 2 {
		t.Fatalf("StopAllLive = %d, want 2", n)
	}
	if got := len(m.ActiveStreams()); got != 0 {
		t.Errorf("after StopAllLive, ActiveStreams = %d, want 0", got)
	}
	if !alive(m, "clip-c") {
		t.Error("clip must not be stopped by StopAllLive")
	}
}
