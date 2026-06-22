package message

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type fixture struct {
	Name    string `json:"name"`
	Options struct {
		Gateway             string  `json:"gateway"`
		Port                int     `json:"port"`
		Transmission        string  `json:"transmission"`
		TimezoneOffsetHours float64 `json:"timezoneOffsetHours"`
	} `json:"options"`
	Input struct {
		Serial  string `json:"serial"`
		Make    string `json:"make"`
		Model   string `json:"model"`
		Type    string `json:"type"`
		Port    int    `json:"port"`
		Network struct {
			RemoteAddress string `json:"remoteAddress"`
			RemotePort    int    `json:"remotePort"`
		} `json:"network"`
		Payload    map[string]any `json:"payload"`
		ReceivedAt string         `json:"receivedAt"`
	} `json:"input"`
}

type goldenEntry struct {
	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`
}

// TestUniversalGoldenParity proves the Go Build() output is identical to the
// production JS buildUniversalWebhookMessage for every fixture. Regenerate the
// golden file with: node tools/gen-webhook-golden.mjs
func TestUniversalGoldenParity(t *testing.T) {
	fixtures := loadJSON[[]fixture](t, "testdata/fixtures.json")
	goldens := loadJSON[[]goldenEntry](t, "testdata/golden.json")
	if len(fixtures) != len(goldens) {
		t.Fatalf("fixture/golden count mismatch: %d vs %d", len(fixtures), len(goldens))
	}

	// A single builder shared across fixtures so the per-device seq_no counter
	// advances exactly as the JS module-level counter did when golden was built.
	b := NewBuilder("", 0)

	for i, fx := range fixtures {
		gold := goldens[i]
		if fx.Name != gold.Name {
			t.Fatalf("fixture[%d] name %q != golden name %q", i, fx.Name, gold.Name)
		}
		b.SetGateway(fx.Options.Gateway)
		b.TZOffsetHrs = fx.Options.TimezoneOffsetHours

		in := Inbound{
			Serial:     fx.Input.Serial,
			Make:       fx.Input.Make,
			Model:      fx.Input.Model,
			Type:       fx.Input.Type,
			Port:       fx.Input.Port,
			Network:    Network{RemoteAddress: fx.Input.Network.RemoteAddress, RemotePort: fx.Input.Network.RemotePort},
			Payload:    fx.Input.Payload,
			ReceivedAt: fx.Input.ReceivedAt,
		}

		got := normalize(t, b.Build(in))
		want := normalizeRaw(t, gold.Output)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fixture %q: Go output differs from JS golden\n--- got ---\n%s\n--- want ---\n%s",
				fx.Name, mustIndent(got), mustIndent(want))
		}
	}
}

func loadJSON[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return v
}

func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return normalizeRaw(t, b)
}

func normalizeRaw(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}

func mustIndent(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
