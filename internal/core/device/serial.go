// Package device provides device identity helpers and pluggable authorization.
package device

import (
	"regexp"
	"strings"
)

var whitespace = regexp.MustCompile(`\s+`)

// NormalizeSerial mirrors the JS gateway: collapse whitespace runs to a single
// underscore and uppercase. e.g. "MVR5452 4064668" -> "MVR5452_4064668".
// Empty or all-whitespace input returns "unknown".
func NormalizeSerial(serial string) string {
	if strings.TrimSpace(serial) == "" {
		return "unknown"
	}
	return strings.ToUpper(whitespace.ReplaceAllString(serial, "_"))
}
