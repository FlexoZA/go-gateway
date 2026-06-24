package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
)

func multiUnitServer(f *fakeData) *Server {
	units := []UnitInfo{
		{Name: "howen", Caps: gateway.EffectiveCapabilities{HasVideo: true, HasConfig: true}},
		{Name: "fleetiger", Caps: gateway.EffectiveCapabilities{HasMappings: true}, Schema: []gateway.SettingField{
			{Key: "timezone_offset_hours", Label: "TZ", Type: "number", Default: "0"},
		}},
	}
	return New("127.0.0.1", 0, units, stubVerifier{valid: "k"}, f, nil, logging.New("test"))
}

func TestGatewayInfoListsUnits(t *testing.T) {
	s := multiUnitServer(&fakeData{})
	rec := do(s, "GET", "/api/gateway/info", "")
	if rec.Code != 200 {
		t.Fatalf("gateway/info = %d", rec.Code)
	}
	var resp struct {
		Unit  string     `json:"unit"`
		Units []UnitInfo `json:"units"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Units) != 2 {
		t.Fatalf("units length = %d, want 2", len(resp.Units))
	}
	if resp.Unit != "howen" {
		t.Fatalf("back-compat unit = %q, want howen", resp.Unit)
	}
	if resp.Units[1].Name != "fleetiger" {
		t.Fatalf("units[1] = %q, want fleetiger", resp.Units[1].Name)
	}
}

func TestMappingsRequireUnitWhenMultiUnit(t *testing.T) {
	s := multiUnitServer(&fakeData{})
	if rec := do(s, "GET", "/api/mappings", ""); rec.Code != 400 {
		t.Fatalf("mappings without unit = %d, want 400", rec.Code)
	}
	if rec := do(s, "GET", "/api/mappings?unit=fleetiger", ""); rec.Code != 200 {
		t.Fatalf("mappings?unit=fleetiger = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	if rec := do(s, "GET", "/api/mappings?unit=nope", ""); rec.Code != 404 {
		t.Fatalf("mappings?unit=nope = %d, want 404", rec.Code)
	}
}

func TestUnitSettingsRoutes(t *testing.T) {
	s := multiUnitServer(&fakeData{})

	rec := do(s, "GET", "/api/unit-types/fleetiger/settings/schema", "")
	if rec.Code != 200 {
		t.Fatalf("schema = %d", rec.Code)
	}
	var sch struct {
		Schema []gateway.SettingField `json:"schema"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sch); err != nil {
		t.Fatal(err)
	}
	if len(sch.Schema) != 1 || sch.Schema[0].Key != "timezone_offset_hours" {
		t.Fatalf("schema = %+v", sch.Schema)
	}

	// Unknown unit → 404.
	if rec := do(s, "GET", "/api/unit-types/nope/settings/schema", ""); rec.Code != 404 {
		t.Fatalf("unknown unit schema = %d, want 404", rec.Code)
	}

	// Valid number value.
	if rec := do(s, "PUT", "/api/unit-types/fleetiger/settings", `{"key":"timezone_offset_hours","value":"2"}`); rec.Code != 200 {
		t.Fatalf("set valid = %d (%s)", rec.Code, rec.Body)
	}
	// Invalid number value.
	if rec := do(s, "PUT", "/api/unit-types/fleetiger/settings", `{"key":"timezone_offset_hours","value":"abc"}`); rec.Code != 400 {
		t.Fatalf("set invalid number = %d, want 400", rec.Code)
	}
	// Unknown key.
	if rec := do(s, "PUT", "/api/unit-types/fleetiger/settings", `{"key":"nope","value":"1"}`); rec.Code != 400 {
		t.Fatalf("set unknown key = %d, want 400", rec.Code)
	}
}
