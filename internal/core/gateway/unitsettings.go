package gateway

import (
	"strconv"
	"strings"
	"sync/atomic"
)

// UnitSettings is a unit's live, editable gateway-side settings (see
// ConfigurableUnit / SettingField). The app runner seeds it from the unit's schema
// defaults, loads stored values from the database, and Replaces it on every admin
// edit (via LISTEN/NOTIFY). A session reads it on the hot path through typed
// getters; reads are lock-free (atomic pointer load), matching the mapping
// hot-reload pattern used by the howen unit.
type UnitSettings struct {
	v atomic.Pointer[map[string]string]
}

// NewUnitSettings returns an empty settings holder.
func NewUnitSettings() *UnitSettings {
	us := &UnitSettings{}
	m := map[string]string{}
	us.v.Store(&m)
	return us
}

// Replace atomically swaps in a new set of values (a defensive copy is stored).
func (u *UnitSettings) Replace(m map[string]string) {
	cp := make(map[string]string, len(m))
	for k, val := range m {
		cp[k] = val
	}
	u.v.Store(&cp)
}

// lookup returns the trimmed raw value and whether it is present and non-empty.
func (u *UnitSettings) lookup(key string) (string, bool) {
	if u == nil {
		return "", false
	}
	p := u.v.Load()
	if p == nil {
		return "", false
	}
	v, ok := (*p)[key]
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}

// String returns the setting value, or def when unset/empty.
func (u *UnitSettings) String(key, def string) string {
	if v, ok := u.lookup(key); ok {
		return v
	}
	return def
}

// Float returns the setting parsed as a float, or def when unset or unparseable.
func (u *UnitSettings) Float(key string, def float64) float64 {
	if v, ok := u.lookup(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// Bool returns the setting parsed as a bool, or def when unset or unrecognized.
func (u *UnitSettings) Bool(key string, def bool) bool {
	if v, ok := u.lookup(key); ok {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
