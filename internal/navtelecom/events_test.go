package navtelecom

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

func TestEventForCodeDefault(t *testing.T) {
	// Reserved current-state id → no event.
	if ev, ok := eventForCode(eventCurrentState); ok {
		t.Fatalf("current-state id produced an event %q", ev)
	}
	// Unmapped real event → raw passthrough so it isn't lost.
	if ev, ok := eventForCode(5899); !ok || ev != "NTC:5899" {
		t.Fatalf("eventForCode(5899) = %q,%v, want NTC:5899,true", ev, ok)
	}
}

func TestApplyMappingsOverridesAndResets(t *testing.T) {
	defer ApplyMappings(nil) // restore the built-in default for other tests

	ApplyMappings(mapping.ByModel{"": mapping.Table{
		mapTypeEventCode: {5899: "PANIC", 100: "IGNITION:ON"},
	}})
	if ev, _ := eventForCode(5899); ev != "PANIC" {
		t.Fatalf("mapped 5899 = %q, want PANIC", ev)
	}
	if ev, _ := eventForCode(100); ev != "IGNITION:ON" {
		t.Fatalf("mapped 100 = %q, want IGNITION:ON", ev)
	}
	// An unmapped code still passes through.
	if ev, _ := eventForCode(42); ev != "NTC:42" {
		t.Fatalf("unmapped 42 = %q, want NTC:42", ev)
	}

	// Reset → back to passthrough.
	ApplyMappings(nil)
	if ev, _ := eventForCode(5899); ev != "NTC:5899" {
		t.Fatalf("after reset 5899 = %q, want NTC:5899", ev)
	}
}

func TestDefaultMappingEntriesEmpty(t *testing.T) {
	// Navtelecom ships no built-in codes (they're fleet-specific, added in admin).
	if got := DefaultMappingEntries(); len(got) != 0 {
		t.Fatalf("DefaultMappingEntries = %v, want empty", got)
	}
}
