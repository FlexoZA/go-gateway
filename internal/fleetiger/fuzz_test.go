package fleetiger

import (
	"encoding/hex"
	"strings"
	"testing"
)

func addHexSeeds(f *testing.F, seeds ...string) {
	f.Helper()
	for _, s := range seeds {
		b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
		if err != nil {
			f.Fatalf("bad seed hex %q: %v", s, err)
		}
		f.Add(b)
	}
}

// FuzzParseGt06Packet feeds arbitrary bytes to the GT06 frame parser. It must
// always return a packet or an error and never panic — this is the entry point
// for untrusted device input, and it also exercises the GPS/LBS/status/datetime
// sub-decoders through their real (length-gated) call paths.
func FuzzParseGt06Packet(f *testing.F) {
	addHexSeeds(f,
		"78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A",                                                       // spec login
		"78 78 1F 12 0B 08 1D 11 2E 10 CF 02 7A C7 EB 0C 46 58 49 00 14 8F 01 CC 00 28 7D 00 1F B8 00 03 80 81 0D 0A", // spec location
		"78 78",                         // start bits only
		"78 78 05 13 00 01 00 00 0D 0A", // heartbeat-ish
	)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseGt06Packet(data, 2.0)
	})
}
