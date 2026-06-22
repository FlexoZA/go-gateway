package eventcodes

import "testing"

func TestStandardParses(t *testing.T) {
	codes := Standard()
	if len(codes) < 100 {
		t.Fatalf("expected >100 codes, got %d", len(codes))
	}

	byCode := map[string]Code{}
	for _, c := range codes {
		if c.Code == "" {
			t.Fatalf("empty code in list")
		}
		if c.Category == "" {
			t.Fatalf("code %q has no category (forward-fill failed)", c.Code)
		}
		byCode[c.Code] = c
	}

	// Spot-check a first-in-category row and a continuation row share/forward-fill.
	if c, ok := byCode["PANIC"]; !ok || c.Category != "Panic" {
		t.Fatalf("PANIC missing/wrong category: %+v", c)
	}
	if c, ok := byCode["SPEEDING:END"]; !ok || c.Category != "Speeding" {
		t.Fatalf("SPEEDING:END should forward-fill category Speeding: %+v", c)
	}
	if _, ok := byCode["AI:CELLPHONE"]; !ok {
		// AI:CELLPHONE is used by the Howen dms_adas default; it should exist.
		t.Logf("note: AI:CELLPHONE not in standard list")
	}
	// The header row must never leak in as a code.
	if _, ok := byCode["Event Code"]; ok {
		t.Fatal("header row leaked into codes")
	}
}
