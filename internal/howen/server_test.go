package howen

import "testing"

// TestModelFromFirmware pins the fw→model derivation. The ME40 reports a
// firmware-versioned string ("ME40-02V8") whose "V<n>" suffix must be stripped
// so per-model mappings survive firmware bumps, while the established MC30
// string ("MC30-02H") and any unrecognised firmware pass through unchanged.
func TestModelFromFirmware(t *testing.T) {
	cases := []struct {
		fw   string
		want string
	}{
		{"ME40-02V8", "ME40-02"},     // strip firmware version → stable key
		{"ME40-02V10", "ME40-02"},    // multi-digit version
		{"MC30-02H", "MC30-02H"},     // no V<n> suffix → live MC30 unaffected
		{"MA80-04", "MA80-04"},       // future model, no suffix → verbatim
		{"  ME40-02V8  ", "ME40-02"}, // trimmed before matching
		{"V8", "V8"},                 // never strip to empty
		{"", ""},                     // empty fw stays empty
	}
	for _, c := range cases {
		if got := modelFromFirmware(c.fw); got != c.want {
			t.Errorf("modelFromFirmware(%q) = %q, want %q", c.fw, got, c.want)
		}
	}
}
