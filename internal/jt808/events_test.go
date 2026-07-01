package jt808

import (
	"reflect"
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

func eventsFor(loc location) []string { return testProto.resolveEvents(loc, deviceModel) }

func TestResolveAlarmEvents(t *testing.T) {
	cases := []struct {
		bit  int
		want string
	}{
		{1, "SPEEDING"},
		{29, "COLLISION"},
		{30, "COLLISION:TURN_OVER"},
		{31, "ALARM:DOOR_OPEN"},
		{19, "PARKING:START"},
	}
	for _, c := range cases {
		got := eventsFor(location{Alarm: 1 << uint(c.bit), TLVs: map[byte][]byte{}})
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("alarm bit %d -> %v, want [%s]", c.bit, got, c.want)
		}
	}
}

func TestResolveVendorAndUlvEvents(t *testing.T) {
	// Vendor 0xE1 = harsh acceleration.
	if got := eventsFor(location{TLVs: map[byte][]byte{0xe1: {0x01}}}); len(got) != 1 || got[0] != "HARSH:ACCELERATION" {
		t.Errorf("E1=0x01 -> %v", got)
	}
	// Vendor 0x70 suffix 02 02 00 = ignition on.
	if got := eventsFor(location{TLVs: map[byte][]byte{0x70: {0xaa, 0x02, 0x02, 0x00}}}); len(got) != 1 || got[0] != "IGNITION:ON" {
		t.Errorf("70 suffix -> %v", got)
	}
	// ULV DMS (0x65): 4-byte alarm id then subtype byte 0x01 = fatigue.
	if got := eventsFor(location{TLVs: map[byte][]byte{0x65: {0, 0, 0, 0, 0x01}}}); len(got) != 1 || got[0] != "AI:FATIGUE" {
		t.Errorf("DMS subtype -> %v", got)
	}
	// ULV ADAS (0x64) subtype 0x01 = collision.
	if got := eventsFor(location{TLVs: map[byte][]byte{0x64: {0, 0, 0, 0, 0x01}}}); len(got) != 1 || got[0] != "COLLISION" {
		t.Errorf("ADAS subtype -> %v", got)
	}
}

func TestResolveDedup(t *testing.T) {
	// Collision alarm bit + ADAS forward-collision both map to COLLISION: dedup.
	loc := location{Alarm: 1 << 29, TLVs: map[byte][]byte{0x64: {0, 0, 0, 0, 0x01}}}
	if got := eventsFor(loc); len(got) != 1 || got[0] != "COLLISION" {
		t.Fatalf("dedup -> %v, want [COLLISION]", got)
	}
}

func TestNoEventForPlainGps(t *testing.T) {
	if got := eventsFor(location{TLVs: map[byte][]byte{}}); len(got) != 0 {
		t.Fatalf("plain gps -> %v, want none", got)
	}
}

func TestDefaultMappingEntries(t *testing.T) {
	entries := DefaultMappingEntries()
	if len(entries) == 0 {
		t.Fatal("no default mapping entries")
	}
	var sawAlarm bool
	for _, e := range entries {
		if e.MapType == mapTypeAlarm && e.Code == 29 && e.EventCode == "COLLISION" {
			sawAlarm = true
		}
	}
	if !sawAlarm {
		t.Fatal("expected alarm bit 29 -> COLLISION in defaults")
	}
}

func TestApplyMappingsOverride(t *testing.T) {
	t.Cleanup(func() { testProto.ApplyMappings(nil) }) // restore built-in defaults
	testProto.ApplyMappings(mapping.ByModel{
		"": mapping.Table{mapTypeAlarm: {29: "CUSTOM:CRASH"}},
	})
	if got := eventsFor(location{Alarm: 1 << 29, TLVs: map[byte][]byte{}}); !reflect.DeepEqual(got, []string{"CUSTOM:CRASH"}) {
		t.Fatalf("override -> %v, want [CUSTOM:CRASH]", got)
	}
	// A map_type not present in the loaded table keeps its built-in default.
	if got := eventsFor(location{TLVs: map[byte][]byte{0xe1: {0x01}}}); len(got) != 1 || got[0] != "HARSH:ACCELERATION" {
		t.Fatalf("untouched map_type -> %v", got)
	}
	testProto.ApplyMappings(nil)
	if got := eventsFor(location{Alarm: 1 << 29, TLVs: map[byte][]byte{}}); len(got) != 1 || got[0] != "COLLISION" {
		t.Fatalf("after reset -> %v, want [COLLISION]", got)
	}
}

func TestResolveEventsTrace(t *testing.T) {
	// A mapped alarm bit plus an unmapped one: the mapped bit resolves (source
	// "table") and the unmapped bit is recorded (source "fallback", empty code) so
	// the Mapping Test can flag it. bit 29 -> COLLISION; bit 2 is not mapped.
	loc := location{Alarm: (1 << 29) | (1 << 2), TLVs: map[byte][]byte{}}
	events, trace := testProto.resolveEventsTrace(loc, deviceModel)
	if len(events) != 1 || events[0] != "COLLISION" {
		t.Fatalf("events = %v, want [COLLISION]", events)
	}
	var mapped, unmapped *mapTraceEntry
	for i := range trace {
		switch trace[i].Code {
		case 29:
			mapped = &trace[i]
		case 2:
			unmapped = &trace[i]
		}
	}
	if mapped == nil || mapped.MapType != mapTypeAlarm || mapped.EventCode != "COLLISION" || mapped.Source != traceTable {
		t.Fatalf("mapped trace entry = %+v", mapped)
	}
	if unmapped == nil || unmapped.EventCode != "" || unmapped.Source != traceFallback {
		t.Fatalf("unmapped trace entry = %+v (want fallback, empty code)", unmapped)
	}
	// Plain GPS has no raw alarm signal, so no trace (no event_forward is emitted).
	if _, tr := testProto.resolveEventsTrace(location{TLVs: map[byte][]byte{}}, deviceModel); len(tr) != 0 {
		t.Fatalf("plain GPS produced trace %v", tr)
	}
}

func TestResolveTraceSkipsIdleTLV(t *testing.T) {
	// A DMS TLV with subtype 0 is idle status, not an alarm — it must produce no
	// trace (so no spurious "unmapped code 0" in the Mapping Test).
	if _, tr := testProto.resolveEventsTrace(location{TLVs: map[byte][]byte{0x65: {0, 0, 0, 0, 0x00}}}, deviceModel); len(tr) != 0 {
		t.Fatalf("idle DMS subtype 0 produced trace %v", tr)
	}
	// A nonzero-but-unmapped subtype IS surfaced (fallback) so the operator can map it.
	_, tr := testProto.resolveEventsTrace(location{TLVs: map[byte][]byte{0x65: {0, 0, 0, 0, 0x07}}}, deviceModel)
	if len(tr) != 1 || tr[0].MapType != mapTypeDms || tr[0].Code != 0x07 || tr[0].Source != traceFallback {
		t.Fatalf("unmapped DMS subtype 7 trace = %+v", tr)
	}
}
