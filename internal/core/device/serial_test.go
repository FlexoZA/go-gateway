package device

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeSerial(t *testing.T) {
	cases := []struct{ in, want string }{
		{"MVR5452 4064668", "MVR5452_4064668"},     // whitespace run -> single underscore
		{"  lots   of  space ", "_LOTS_OF_SPACE_"}, // edge whitespace -> underscores (JS-mirror behavior, unchanged)
		{"abc123", "ABC123"},                       // uppercased
		{"keep-me_ok", "KEEP-ME_OK"},
		{"", "unknown"},
		{"   ", "unknown"},
		// Path-hostile input must be neutralized to a single safe segment.
		{"../../etc/passwd", "______ETC_PASSWD"},
		{"a/b/c", "A_B_C"},
		{`a\b`, "A_B"},
		{"..", "__"},
		{"x.y", "X_Y"},
	}
	for _, c := range cases {
		if got := NormalizeSerial(c.in); got != c.want {
			t.Errorf("NormalizeSerial(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeSerialStaysInBucket is the property that matters: whatever a
// device sends, the normalized serial is a single path segment that cannot
// escape a bucket root when joined to it.
func TestNormalizeSerialStaysInBucket(t *testing.T) {
	root := "/var/lib/gateway/clips"
	hostile := []string{
		"../../etc/passwd",
		"../../../root",
		"a/../../b",
		`..\..\windows`,
		"/abs/path",
		"normal..serial",
	}
	for _, in := range hostile {
		s := NormalizeSerial(in)
		if strings.ContainsAny(s, `/\`) || strings.Contains(s, "..") {
			t.Errorf("NormalizeSerial(%q) = %q still contains a path separator or ..", in, s)
		}
		full := filepath.Join(root, s, "file.mp4")
		if rel, err := filepath.Rel(root, full); err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("NormalizeSerial(%q) = %q escapes the bucket: join -> %q", in, s, full)
		}
	}
}
