package navtelecom

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

func TestEventForCodeDefault(t *testing.T) {
	// Reserved current-state id → routine, no event.
	if ev, ok := eventForCode(eventCurrentState); ok {
		t.Fatalf("current-state id produced an event %q", ev)
	}
	// GPS_TIMER (5899) and the timer family are routine → no event.
	if ev, ok := eventForCode(5899); ok {
		t.Fatalf("GPS_TIMER produced an event %q, want routine/no event", ev)
	}
	if ev, ok := eventForCode(5634); ok {
		t.Fatalf("timer 5634 produced an event %q, want routine", ev)
	}
	// A seeded default code maps to its ACM code.
	if ev, ok := eventForCode(4688); !ok || ev != "IGNITION:ON" {
		t.Fatalf("eventForCode(4688) = %q,%v, want IGNITION:ON,true", ev, ok)
	}
	if ev, _ := eventForCode(5904); ev != "ZONE:ENTER" {
		t.Fatalf("eventForCode(5904) = %q, want ZONE:ENTER", ev)
	}
	// An unmapped, non-routine code passes through so it isn't lost.
	if ev, ok := eventForCode(9999); !ok || ev != "NTC:9999" {
		t.Fatalf("eventForCode(9999) = %q,%v, want NTC:9999,true", ev, ok)
	}
}

func TestApplyMappingsOverridesAndResets(t *testing.T) {
	defer ApplyMappings(nil) // restore the built-in default for other tests

	// An admin mapping overrides both the default and the routine classification.
	ApplyMappings(mapping.ByModel{"": mapping.Table{
		mapTypeEventCode: {4688: "ENGINE:ON", 5899: "TRIP:START"},
	}})
	if ev, _ := eventForCode(4688); ev != "ENGINE:ON" {
		t.Fatalf("override 4688 = %q, want ENGINE:ON", ev)
	}
	if ev, ok := eventForCode(5899); !ok || ev != "TRIP:START" {
		t.Fatalf("override 5899 = %q,%v, want TRIP:START (override beats routine)", ev, ok)
	}

	// Reset → defaults restored, routine classification back.
	ApplyMappings(nil)
	if ev, _ := eventForCode(4688); ev != "IGNITION:ON" {
		t.Fatalf("after reset 4688 = %q, want IGNITION:ON", ev)
	}
	if _, ok := eventForCode(5899); ok {
		t.Fatal("after reset 5899 should be routine (no event)")
	}
}

func TestDefaultMappingEntries(t *testing.T) {
	entries := DefaultMappingEntries()
	if len(entries) != len(defaultEventCodes) {
		t.Fatalf("entries = %d, want %d", len(entries), len(defaultEventCodes))
	}
	// Entries are sorted by code and carry the ACM code + source mnemonic.
	var sawIgnOn bool
	for i, e := range entries {
		if e.MapType != mapTypeEventCode {
			t.Fatalf("entry %d map_type = %q", i, e.MapType)
		}
		if i > 0 && entries[i-1].Code > e.Code {
			t.Fatalf("entries not sorted by code at %d", i)
		}
		if e.Code == 4688 {
			sawIgnOn = true
			if e.EventCode != "IGNITION:ON" || e.Description == "" {
				t.Fatalf("4688 entry = %+v, want IGNITION:ON with a description", e)
			}
		}
	}
	if !sawIgnOn {
		t.Fatal("default entries missing 4688 IGN_ON")
	}
}
