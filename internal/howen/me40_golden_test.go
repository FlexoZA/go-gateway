package howen

import "testing"

// Live ME40-02 (fw "ME40-02V8", serial 83209490) frames captured off the wire on
// the staging lab, 2026-07-02.
const (
	// 0x1041 GPS status.
	me40GpsStatusHex = "1c7374617475732d38333230393439302d3139463231384341413144001a0702083328ad0010011a0702052013000a0000010608001d235d0200e78ed60800010000001f000101000107000009050000010f01dc720700000000000000"
	// 0x1051 ignition-on alarm (ec=31, det=null).
	me40IgnitionOnAlarmHex = "1b616c61726d2d38333230393439302d313946323138434141314400c20000007b22646574223a6e756c6c2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30372d30322030383a35333a3436222c226563223a223331222c226574223a22323032362d30372d30322030383a35333a3436222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30372d30322030383a35333a3436222c2275756964223a223137383239383234323630303830303130303030303030303833323039343930227d0a001a070208352ead0011011a070208352e410f00004e0607001d5a5e0600e78c910800010000001f000101000107000008050000010f01dc720700000000000000"
)

// TestME40GpsStatus decodes a real ME40 GPS status frame. The ME40 shares the
// Howen H-Protocol with the MC30, so the existing decoder handles it unchanged.
func TestME40GpsStatus(t *testing.T) {
	sp := parseHowenStatusPayload(mustHex(t, me40GpsStatusHex))
	if sp == nil || sp.Status == nil || sp.Status.Location == nil {
		t.Fatalf("gps parse: %#v", sp)
	}
	loc := sp.Status.Location
	if !approx(loc.Latitude, -25.965356666666665) {
		t.Errorf("lat = %v", loc.Latitude)
	}
	if !approx(loc.Longitude, 29.258191666666665) {
		t.Errorf("lon = %v", loc.Longitude)
	}
	if loc.Satellites != 10 || loc.Positioning != 1 {
		t.Errorf("sats=%d pos=%d", loc.Satellites, loc.Positioning)
	}
}

// TestME40IgnitionOnAlarm decodes a real ME40 alarm frame and confirms it maps
// under the shared default table (the ME40 needs no per-model mapping table:
// its alarm codes are protocol-level, not model-specific).
func TestME40IgnitionOnAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, me40IgnitionOnAlarmHex))
	if ap == nil {
		t.Fatal("alarm parse nil")
	}
	if got := mapHowenEventCodes("ME40-02", ap.EC, ap.Detail, ap.Alarm); len(got) != 1 || got[0] != "IGNITION:ON" {
		t.Fatalf("events = %v, want [IGNITION:ON]", got)
	}
	if ap.Status == nil || ap.Status.Location == nil || ap.Status.Location.Positioning != 1 {
		t.Fatalf("missing/unpositioned status location: %#v", ap.Status)
	}
}
