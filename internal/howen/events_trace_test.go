package howen

import "testing"

// TestMapHowenEventCodesTrace checks that the decode trace records the right
// table, sub-code, resolved event, and source for each kind of resolution —
// the data the live mapping test mode renders ("bit 1 fired → COLLISION") and
// uses to flag unmapped device codes.
func TestMapHowenEventCodesTrace(t *testing.T) {
	cases := []struct {
		name      string
		code      any
		detail    map[string]any
		wantEvent string
		wantType  string
		wantCode  int
		wantSrc   string
	}{
		// Table-driven: a g-sensor direction that maps to COLLISION.
		{"vibration→collision", 12, map[string]any{"dt": "4"}, "COLLISION", "vibration_direction", 4, traceTable},
		// Table-driven: DMS/ADAS type that maps to a cellphone alert.
		{"dms→cellphone", 30, map[string]any{"tp": "34"}, "AI:CELLPHONE", "dms_adas", 34, traceTable},
		// Builtin rule: trip start has no editable table.
		{"builtin trip", 41, nil, "TRIP:START", "", 41, traceBuiltin},
		// Fallback: an unknown DMS type that no row maps — flagged for the operator.
		{"dms unmapped", 30, map[string]any{"tp": "9999"}, "ALARM", "dms_adas", 9999, traceFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, trace := mapHowenEventCodesTrace("", tc.code, tc.detail, nil)
			if len(events) == 0 || events[0] != tc.wantEvent {
				t.Fatalf("events = %v, want first %q", events, tc.wantEvent)
			}
			if len(trace) != 1 {
				t.Fatalf("trace = %v, want exactly 1 entry", trace)
			}
			got := trace[0]
			if got.EventCode != tc.wantEvent || got.MapType != tc.wantType || got.Code != tc.wantCode || got.Source != tc.wantSrc {
				t.Fatalf("trace = %+v, want {EventCode:%q MapType:%q Code:%d Source:%q}",
					got, tc.wantEvent, tc.wantType, tc.wantCode, tc.wantSrc)
			}
		})
	}
}
