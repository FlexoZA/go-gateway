package cathexis

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

func TestDefaultMappingEntries(t *testing.T) {
	entries := DefaultMappingEntries()
	if len(entries) != len(defaultCathexisEvents) {
		t.Fatalf("got %d entries, want %d", len(entries), len(defaultCathexisEvents))
	}
	byCode := map[int]mapping.Entry{}
	for _, e := range entries {
		if e.MapType != mapTypeEvent {
			t.Fatalf("map_type = %q, want %q", e.MapType, mapTypeEvent)
		}
		byCode[e.Code] = e
	}
	// Code 3 is harsh_braking → HARSH:BRAKING, with the device name in Description.
	if e := byCode[3]; e.EventCode != "HARSH:BRAKING" || e.Description != "harsh_braking" {
		t.Fatalf("code 3 = %+v, want HARSH:BRAKING / harsh_braking", e)
	}
}

// TestRealDeviceEventNames pins the real event names an MVR5 emits (API §4.1 +
// the live unit's eventpreviews list) to their canonical ACM codes. Before the
// mapping overhaul these names had no entry and resolved to "ALARM".
func TestRealDeviceEventNames(t *testing.T) {
	cases := map[string]string{
		"idle_starts":       "IDLING:START",
		"idle_stops":        "IDLING:END",
		"idle_periodic":     "IDLING:PERIODIC",
		"speeding":          "SPEEDING",
		"gps_lock":          "GPS:LOCKED",
		"gps_lost":          "GPS:TIMEOUT",
		"button_pressed":    "PANIC",
		"fatigue":           "AI:FATIGUE",
		"distraction":       "AI:DISTRACTION",
		"seatbelt":          "AI:SEATBELT",
		"yawn":              "AI:YAWN",
		"cellphone":         "AI:CELLPHONE",
		"passenger":         "AI:PASSENGER",
		"tamper":            "AI:TAMPER",
		"followingdistance": "FOLLOWING:DISTANCE:VIOLATION",
		"call_started":      "CALL:STARTED",
		"call_ended":        "CALL:ENDED",
		"deep_sleep":        "SLEEP",
		"entered_standby":   "STANDBY",
		"wake_dapi_on":      "STANDBY:WAKE:DAPI",
		"harsh_impact":      "COLLISION",
	}
	for name, want := range cases {
		got := toStandardEventCodes(map[string]any{"name": name}, true)
		if len(got) != 1 || got[0] != want {
			t.Errorf("event %q mapped to %v, want [%s]", name, got, want)
		}
	}
	// A genuinely unknown name still resolves to ALARM.
	if got := toStandardEventCodes(map[string]any{"name": "no_such_event"}, true); len(got) != 1 || got[0] != "ALARM" {
		t.Errorf("unknown event mapped to %v, want [ALARM]", got)
	}
}

func TestApplyMappingsOverride(t *testing.T) {
	t.Cleanup(func() { currentEventCodes.Store(nil) }) // restore defaults for other tests

	// Override harsh_braking (code 3) to a custom event code.
	ApplyMappings(mapping.ByModel{
		"": mapping.Table{mapTypeEvent: map[int]string{3: "CUSTOM:HARD_BRAKE"}},
	})
	got := toStandardEventCodes(map[string]any{"name": "harsh_braking"}, true)
	if len(got) != 1 || got[0] != "CUSTOM:HARD_BRAKE" {
		t.Fatalf("after override got %v, want [CUSTOM:HARD_BRAKE]", got)
	}

	// A code not in the override falls back to the built-in default.
	got = toStandardEventCodes(map[string]any{"name": "panic"}, true)
	if len(got) != 1 || got[0] != "PANIC" {
		t.Fatalf("got %v, want [PANIC] (built-in fallback)", got)
	}

	// Reset and confirm defaults are back.
	currentEventCodes.Store(nil)
	got = toStandardEventCodes(map[string]any{"name": "harsh_braking"}, true)
	if len(got) != 1 || got[0] != "HARSH:BRAKING" {
		t.Fatalf("after reset got %v, want [HARSH:BRAKING]", got)
	}
}
