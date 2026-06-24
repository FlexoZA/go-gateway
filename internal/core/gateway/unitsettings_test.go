package gateway

import (
	"sync"
	"testing"
)

func TestUnitSettingsGettersAndDefaults(t *testing.T) {
	us := NewUnitSettings()

	// Empty holder → defaults.
	if got := us.String("k", "def"); got != "def" {
		t.Fatalf("String default = %q, want def", got)
	}
	if got := us.Float("tz", 1.5); got != 1.5 {
		t.Fatalf("Float default = %v, want 1.5", got)
	}
	if got := us.Bool("on", true); got != true {
		t.Fatalf("Bool default = %v, want true", got)
	}

	us.Replace(map[string]string{
		"name":  "fleet",
		"tz":    "2",
		"flag":  "true",
		"blank": "  ",
	})
	if got := us.String("name", "def"); got != "fleet" {
		t.Fatalf("String = %q, want fleet", got)
	}
	if got := us.Float("tz", 0); got != 2 {
		t.Fatalf("Float = %v, want 2", got)
	}
	if got := us.Bool("flag", false); got != true {
		t.Fatalf("Bool = %v, want true", got)
	}
	// Blank/whitespace value falls back to the default.
	if got := us.String("blank", "def"); got != "def" {
		t.Fatalf("blank String = %q, want def", got)
	}
	// Unparseable number falls back.
	us.Replace(map[string]string{"tz": "abc"})
	if got := us.Float("tz", 9); got != 9 {
		t.Fatalf("unparseable Float = %v, want 9", got)
	}
}

// TestUnitSettingsConcurrentReplace exercises the atomic swap under concurrent
// reads — must not race (run with -race).
func TestUnitSettingsConcurrentReplace(t *testing.T) {
	us := NewUnitSettings()
	us.Replace(map[string]string{"tz": "0"})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = us.Float("tz", -1)
				}
			}
		}()
	}
	for i := 0; i < 1000; i++ {
		us.Replace(map[string]string{"tz": "2"})
	}
	close(stop)
	wg.Wait()
}

// A nil holder is safe to read (sessions of non-configurable units leave it nil).
func TestUnitSettingsNilSafe(t *testing.T) {
	var us *UnitSettings
	if got := us.Float("tz", 3); got != 3 {
		t.Fatalf("nil Float = %v, want 3", got)
	}
	if got := us.String("k", "d"); got != "d" {
		t.Fatalf("nil String = %q, want d", got)
	}
}
