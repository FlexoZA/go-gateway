package jt808

import (
	"reflect"
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

func eventsFor(loc location) []string { return resolveEvents(loc, deviceModel) }

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
	t.Cleanup(func() { ApplyMappings(nil) }) // restore built-in defaults
	ApplyMappings(mapping.ByModel{
		"": mapping.Table{mapTypeAlarm: {29: "CUSTOM:CRASH"}},
	})
	if got := eventsFor(location{Alarm: 1 << 29, TLVs: map[byte][]byte{}}); !reflect.DeepEqual(got, []string{"CUSTOM:CRASH"}) {
		t.Fatalf("override -> %v, want [CUSTOM:CRASH]", got)
	}
	// A map_type not present in the loaded table keeps its built-in default.
	if got := eventsFor(location{TLVs: map[byte][]byte{0xe1: {0x01}}}); len(got) != 1 || got[0] != "HARSH:ACCELERATION" {
		t.Fatalf("untouched map_type -> %v", got)
	}
	ApplyMappings(nil)
	if got := eventsFor(location{Alarm: 1 << 29, TLVs: map[byte][]byte{}}); len(got) != 1 || got[0] != "COLLISION" {
		t.Fatalf("after reset -> %v, want [COLLISION]", got)
	}
}
