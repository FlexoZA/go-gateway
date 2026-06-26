package howen

import "testing"

// TestBuildDatahubPayload verifies ec=771 OBD/datahub det fields surface as
// universal sensor payload keys + input/output bit strings, as a "gps" message.
func TestBuildDatahubPayload(t *testing.T) {
	detail := map[string]any{
		"rpm": "2400", "spd": "62", "ct": "89", "acc": "35",
		"ds": "1234", "fu": "78", "ml": "4500", "in": "1000", "out": "01",
	}
	p := buildDatahubPayload(nil, detail, "864312087845313")
	if p["name"] != "gps" {
		t.Fatalf("name = %v, want gps", p["name"])
	}
	want := map[string]float64{
		"engine_rpm": 2400, "obd_speed": 62, "coolant_temp_c": 89,
		"accel_pedal_pct": 35, "obd_distance": 1234, "fuel_l": 78,
		"trip_fuel_used_cc": 4500,
	}
	for k, v := range want {
		got, ok := p[k].(float64)
		if !ok || got != v {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}
	if p["inputs"] != "1000" || p["outputs"] != "01" {
		t.Errorf("io = %v/%v", p["inputs"], p["outputs"])
	}
	if p["howen_datahub"] == nil {
		t.Error("raw datahub not retained")
	}
}

func TestIsTelemetryAlarm(t *testing.T) {
	if !isTelemetryAlarm(float64(771)) || !isTelemetryAlarm("771") {
		t.Error("771 should be telemetry")
	}
	if isTelemetryAlarm(float64(30)) {
		t.Error("30 should not be telemetry")
	}
}
