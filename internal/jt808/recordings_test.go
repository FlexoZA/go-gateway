package jt808

import (
	"testing"
	"time"
)

// TestSplitLocalDays: a window is broken into per-local-day sub-windows that
// never cross local midnight, clamped to the requested bounds.
func TestSplitLocalDays(t *testing.T) {
	// SAST (+2). Local midnight 2026-07-06 00:00 = 2026-07-05 22:00 UTC.
	const tz = 2.0
	mustUTC := func(s string) int64 {
		ts, err := time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}
		return ts.Unix()
	}

	// Single local day: one window, unchanged.
	got := splitLocalDays(mustUTC("2026-07-05 22:00:00"), mustUTC("2026-07-06 12:00:00"), tz, 31)
	if len(got) != 1 {
		t.Fatalf("same-day: want 1 window, got %d: %v", len(got), got)
	}

	// A window crossing two local midnights → 3 sub-windows (partial, full, partial),
	// none of which crosses local midnight.
	start := mustUTC("2026-07-05 10:00:00") // 2026-07-05 12:00 SAST
	end := mustUTC("2026-07-07 10:00:00")   // 2026-07-07 12:00 SAST
	got = splitLocalDays(start, end, tz, 31)
	if len(got) != 3 {
		t.Fatalf("cross-day: want 3 windows, got %d: %v", len(got), got)
	}
	if got[0][0] != start {
		t.Errorf("first window must start at requested start")
	}
	if got[len(got)-1][1] != end {
		t.Errorf("last window must end at requested end")
	}
	const day = 86400
	off := int64(tz * 3600)
	for i, w := range got {
		if w[1] <= w[0] {
			t.Errorf("window %d not positive: %v", i, w)
		}
		if w[1]-w[0] > day {
			t.Errorf("window %d longer than a day: %v", i, w)
		}
		// Neither end may sit strictly inside a local day boundary crossing: the
		// local-day index of start and (end-1s) must match.
		ds := (w[0] + off) / day
		de := (w[1] - 1 + off) / day
		if ds != de {
			t.Errorf("window %d crosses local midnight: %v", i, w)
		}
	}

	// maxDays caps the number of windows.
	got = splitLocalDays(mustUTC("2026-01-01 00:00:00"), mustUTC("2026-12-31 00:00:00"), tz, 5)
	if len(got) != 5 {
		t.Fatalf("cap: want 5 windows, got %d", len(got))
	}

	// Empty/inverted window yields nothing.
	if got := splitLocalDays(1000, 1000, tz, 31); len(got) != 0 {
		t.Errorf("empty window: want 0, got %v", got)
	}
}
