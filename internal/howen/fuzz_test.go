package howen

import (
	"encoding/hex"
	"testing"
)

func addHowenSeeds(f *testing.F, seeds ...string) {
	f.Helper()
	for _, s := range seeds {
		b, err := hex.DecodeString(s)
		if err != nil {
			f.Fatalf("bad seed hex: %v", err)
		}
		f.Add(b)
	}
}

// FuzzParseHowenAlarmPayload feeds arbitrary bytes to the alarm decoder, seeded
// with real captured alarm frames. It must never panic; it reaches the binary
// status/alarm sub-decoders through their real call path.
func FuzzParseHowenAlarmPayload(f *testing.F) {
	addHowenSeeds(f, liveVoltageAlarmHex, liveZeroVoltageAlarmHex, liveIgnitionOnAlarmHex,
		liveIgnitionOffAlarmHex, liveDmsCellphoneAlarmHex, liveDiskDetectionAlarmHex)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = parseHowenAlarmPayload(data)
	})
}

// FuzzParseHowenStatusPayload fuzzes the status-frame decoder.
func FuzzParseHowenStatusPayload(f *testing.F) {
	addHowenSeeds(f, liveVoltageAlarmHex, liveIgnitionOnAlarmHex)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = parseHowenStatusPayload(data)
	})
}

// FuzzParseHowenStatusData fuzzes the bitmask-driven binary status parser
// directly (it is designed to handle truncation, returning a partial struct).
func FuzzParseHowenStatusData(f *testing.F) {
	addHowenSeeds(f, liveVoltageAlarmHex, liveDiskDetectionAlarmHex)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = parseHowenStatusData(data)
	})
}

// FuzzReadHowenFrameHeader fuzzes the 8-byte frame header reader.
func FuzzReadHowenFrameHeader(f *testing.F) {
	f.Add([]byte{0x48, 0x4f, 0x00, 0x10, 0x00, 0x00, 0x00, 0x20})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = readHowenFrameHeader(data)
	})
}
