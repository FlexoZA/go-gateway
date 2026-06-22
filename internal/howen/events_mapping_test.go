package howen

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// TestDefaultMappingEntriesRoundTrip flattens the defaults, rebuilds a Table from
// them, applies it, and confirms mapping behaviour is unchanged.
func TestDefaultMappingEntriesRoundTrip(t *testing.T) {
	defer ApplyMappings(nil) // restore defaults for other tests

	entries := DefaultMappingEntries()
	if len(entries) == 0 {
		t.Fatal("no default entries")
	}
	table := mapping.Table{}
	for _, e := range entries {
		if table[e.MapType] == nil {
			table[e.MapType] = map[int]string{}
		}
		table[e.MapType][e.Code] = e.EventCode
	}
	ApplyMappings(table)

	if got := mapHowenEventCodes(30, map[string]any{"tp": "34"}, nil)[0]; got != "AI:CELLPHONE" {
		t.Fatalf("dms 34 = %q after round-trip", got)
	}
	if got := mapHowenEventCodes(19, nil, nil)[0]; got != "IGNITION:OFF" {
		t.Fatalf("event 19 = %q after round-trip", got)
	}
}

// TestApplyMappingsOverride confirms a database-style override changes the live
// mapping, and that resetting restores the built-in default.
func TestApplyMappingsOverride(t *testing.T) {
	defer ApplyMappings(nil)

	ApplyMappings(mapping.Table{
		"dms_adas": {34: "AI:PHONE_OVERRIDE"},
	})
	if got := mapHowenEventCodes(30, map[string]any{"tp": "34"}, nil)[0]; got != "AI:PHONE_OVERRIDE" {
		t.Fatalf("override not applied: %q", got)
	}
	// A map_type absent from the override keeps its built-in default.
	if got := mapHowenEventCodes(19, nil, nil)[0]; got != "IGNITION:OFF" {
		t.Fatalf("untouched map_type changed: %q", got)
	}

	ApplyMappings(nil)
	if got := mapHowenEventCodes(30, map[string]any{"tp": "34"}, nil)[0]; got != "AI:CELLPHONE" {
		t.Fatalf("reset did not restore default: %q", got)
	}
}
