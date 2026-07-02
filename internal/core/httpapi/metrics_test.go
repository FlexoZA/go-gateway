package httpapi

import (
	"encoding/json"
	"runtime"
	"testing"
)

// TestMetricsEndpoint checks the handler answers 200 with always-available
// runtime fields. Host CPU/memory fields are platform-dependent (/proc), so they
// are asserted only when present.
func TestMetricsEndpoint(t *testing.T) {
	s := newAdminServer(&fakeData{})

	rec := do(s, "GET", "/api/metrics", "")
	if rec.Code != 200 {
		t.Fatalf("GET /api/metrics = %d, want 200 (%s)", rec.Code, rec.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// num_cpu and goroutines are always reported (pure runtime, no /proc).
	if got, ok := body["num_cpu"].(float64); !ok || int(got) != runtime.NumCPU() {
		t.Fatalf("num_cpu = %v, want %d", body["num_cpu"], runtime.NumCPU())
	}
	if _, ok := body["goroutines"].(float64); !ok {
		t.Fatalf("goroutines missing/not a number: %v", body["goroutines"])
	}

	// When host memory is readable, the percentage must be within [0,100].
	if pct, ok := body["mem_percent"].(float64); ok {
		if pct < 0 || pct > 100 {
			t.Fatalf("mem_percent = %v, out of [0,100]", pct)
		}
	}
}

func TestClampPct(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-5, 0}, {0, 0}, {42.5, 42.5}, {100, 100}, {150, 100},
	}
	for _, c := range cases {
		if got := clampPct(c.in); got != c.want {
			t.Errorf("clampPct(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRound1(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0}, {1.24, 1.2}, {1.25, 1.3}, {99.96, 100},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
