package howen

import (
	"testing"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// bypassed mirrors bypassedEventCodes for the assertions below.
var bypassed = []int{1, 7, 11, 12, 15, 27, 28, 30, 32, 41, 43, 48, 768}

// TestBypassedEventCodesNotSeeded: codes the switch resolves internally are not
// emitted as editable event_code rows, while honored event_code rows and the
// independent sub-table rows remain.
func TestBypassedEventCodesNotSeeded(t *testing.T) {
	entries := DefaultMappingEntries()
	has := func(mt string, code int) bool {
		for _, e := range entries {
			if e.MapType == mt && e.Code == code {
				return true
			}
		}
		return false
	}

	for _, code := range bypassed {
		if has(mapTypeEventCode, code) {
			t.Errorf("event_code %d should not be seeded (handled by built-in logic)", code)
		}
	}
	for _, code := range []int{2, 19, 31, 62, 773} { // go through the default branch
		if !has(mapTypeEventCode, code) {
			t.Errorf("honored event_code %d should still be seeded", code)
		}
	}
	// Sub-table rows are a different map_type and stay editable (dms_adas 34).
	if !has(mapTypeDmsAdas, 34) {
		t.Error("dms_adas 34 should still be seeded")
	}
}

// TestPrunableEventCodeMappings: the prune spec lists exactly the event_code
// map_type with every bypassed code, so older DBs get cleaned.
func TestPrunableEventCodeMappings(t *testing.T) {
	p := PrunableEventCodeMappings()
	if len(p) != 1 || p[0].MapType != mapTypeEventCode {
		t.Fatalf("unexpected prune spec: %+v", p)
	}
	set := map[int]bool{}
	for _, c := range p[0].Codes {
		set[c] = true
	}
	for _, c := range bypassed {
		if !set[c] {
			t.Errorf("prune list missing bypassed code %d", c)
		}
	}
}

// TestBypassedCodeIgnoresEventCodeOverride documents WHY these rows aren't seeded:
// the switch resolves them with built-in logic, so an event_code override has no
// effect — seeding it would only mislead the admin.
func TestBypassedCodeIgnoresEventCodeOverride(t *testing.T) {
	defer ApplyMappings(nil)
	ApplyMappings(mapping.ByModel{"": {"event_code": {41: "SHOULD_BE_IGNORED"}}})
	if got := mapHowenEventCodes("", 41, nil, nil)[0]; got != "TRIP:START" {
		t.Fatalf("code 41 = %q, want TRIP:START (event_code override must not apply)", got)
	}
}
