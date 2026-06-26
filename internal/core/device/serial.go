// Package device provides device identity helpers and pluggable authorization.
package device

import (
	"regexp"
	"strings"
)

var whitespace = regexp.MustCompile(`\s+`)

// unsafeSerialChar matches anything outside the safe serial alphabet. The
// normalized serial is used as a single filesystem path segment (the HLS
// directory and the recorded-clip bucket), so it must never contain a path
// separator or "..". Real device serials are alphanumeric (plus _ and -), so
// this replacement is a no-op for them; it only neutralizes hostile input.
var unsafeSerialChar = regexp.MustCompile(`[^A-Z0-9_-]`)

// NormalizeSerial mirrors the JS gateway: collapse whitespace runs to a single
// underscore and uppercase. e.g. "MVR5452 4064668" -> "MVR5452_4064668".
// Empty or all-whitespace input returns "unknown".
//
// As defense in depth it also replaces any character outside [A-Z0-9_-] with an
// underscore, so a serial like "../../etc/x" can never escape its bucket when
// used as a path segment. The serial is the one attacker-supplied value that
// becomes a filesystem path, and this is its single chokepoint.
func NormalizeSerial(serial string) string {
	if strings.TrimSpace(serial) == "" {
		return "unknown"
	}
	s := strings.ToUpper(whitespace.ReplaceAllString(serial, "_"))
	return unsafeSerialChar.ReplaceAllString(s, "_")
}
