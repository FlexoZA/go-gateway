package logging

import "testing"

func TestRingCapturesInfoNotDebugByDefault(t *testing.T) {
	l := New("gateway")
	child := l.With("tcp/fleetiger")

	l.Info(map[string]any{"event": "starting"})
	child.Debug(map[string]any{"event": "gps_forward", "serial": "868"}) // dropped (default info)

	entries, cursor := l.LiveSince(0, "debug", "", "", 100)
	if len(entries) != 1 || entries[0].Fields["event"] != "starting" {
		t.Fatalf("default capture should keep info, drop debug; got %+v", entries)
	}
	if cursor == 0 {
		t.Fatal("cursor should advance")
	}
}

func TestRingCapturesDebugWhenLevelLowered(t *testing.T) {
	l := New("gateway")
	child := l.With("tcp/fleetiger")
	l.SetCaptureLevel("debug")
	if l.CaptureLevel() != "debug" {
		t.Fatalf("CaptureLevel = %q", l.CaptureLevel())
	}

	child.Debug(map[string]any{"event": "gps_forward", "serial": "868"})
	l.With("tcp/howen").Info(map[string]any{"event": "device_approved"})

	// Shared ring across With() children: both lines are visible from any handle.
	entries, _ := child.LiveSince(0, "debug", "", "", 100)
	if len(entries) != 2 {
		t.Fatalf("expected 2 captured entries, got %d", len(entries))
	}

	// Unit filter narrows to a namespace substring.
	fleet, _ := l.LiveSince(0, "debug", "fleetiger", "", 100)
	if len(fleet) != 1 || fleet[0].NS != "tcp/fleetiger" {
		t.Fatalf("unit filter = %+v", fleet)
	}

	// Free-text filter matches a field value.
	hits, _ := l.LiveSince(0, "debug", "", "approved", 100)
	if len(hits) != 1 || hits[0].Fields["event"] != "device_approved" {
		t.Fatalf("q filter = %+v", hits)
	}
}

func TestRingSinceCursorAdvances(t *testing.T) {
	l := New("gateway")
	l.Info(map[string]any{"event": "a"})
	first, cursor := l.LiveSince(0, "info", "", "", 100)
	if len(first) != 1 {
		t.Fatalf("first poll = %d", len(first))
	}
	// Nothing new since the cursor.
	none, _ := l.LiveSince(cursor, "info", "", "", 100)
	if len(none) != 0 {
		t.Fatalf("expected no new entries after cursor, got %d", len(none))
	}
	l.Info(map[string]any{"event": "b"})
	next, _ := l.LiveSince(cursor, "info", "", "", 100)
	if len(next) != 1 || next[0].Fields["event"] != "b" {
		t.Fatalf("expected only the new entry, got %+v", next)
	}
}

func TestRingLevelFilter(t *testing.T) {
	l := New("gateway")
	l.Info(map[string]any{"event": "info1"})
	l.Error(map[string]any{"event": "err1"})
	errsOnly, _ := l.LiveSince(0, "error", "", "", 100)
	if len(errsOnly) != 1 || errsOnly[0].Level != "error" {
		t.Fatalf("error filter = %+v", errsOnly)
	}
}
