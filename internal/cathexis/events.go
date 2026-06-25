package cathexis

import (
	"strings"
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// Cathexis devices report events by string name (e.g. "harsh_braking"), but the
// editable mapping system is integer-keyed. So each known device event gets a
// stable synthetic integer code: the device name → synthetic code → editable
// event_code. The name→code table is fixed in code (the universe of firmware
// events); the code→event_code map is admin-editable via /api/mappings.
//
// Codes are APPEND-ONLY and must never be renumbered — saved mapping rows are
// keyed by them. Multiple device names intentionally map to the same default
// event (e.g. over_speed / overspeeding → SPEEDING), each its own editable row.

const mapTypeEvent = "event"

type cathexisEvent struct {
	code  int
	name  string // sanitized device event name (matches sanitizeEventKey output)
	event string // default ACM standard event code
}

var defaultCathexisEvents = []cathexisEvent{
	{1, "ignition_on", "IGNITION:ON"},
	{2, "ignition_off", "IGNITION:OFF"},
	{3, "harsh_braking", "HARSH:BRAKING"},
	{4, "harsh_acceleration", "HARSH:ACCELERATION"},
	{5, "harsh_turning", "HARSH:CORNERING"},
	{6, "harsh_impact", "COLLISION"},
	{7, "collision_warning", "COLLISION"},
	{8, "rollover_warning", "COLLISION:TURN_OVER"},
	{9, "over_speed", "SPEEDING"},
	{10, "over_speed_warning", "SPEEDING"},
	{11, "overspeeding", "SPEEDING"},
	{12, "panic", "PANIC"},
	{13, "idling_start", "IDLING:START"},
	{14, "idling_end", "IDLING:END"},
}

// nameToCode maps a sanitized device event name to its synthetic code.
var nameToCode = func() map[string]int {
	m := make(map[string]int, len(defaultCathexisEvents))
	for _, e := range defaultCathexisEvents {
		m[e.name] = e.code
	}
	return m
}()

// defaultCodeMap is the built-in synthetic-code → event_code table.
func defaultCodeMap() map[int]string {
	m := make(map[int]string, len(defaultCathexisEvents))
	for _, e := range defaultCathexisEvents {
		m[e.code] = e.event
	}
	return m
}

// currentEventCodes holds the live (possibly admin-overridden) code → event_code
// map. nil until ApplyMappings runs; activeEventCodes falls back to defaults.
var currentEventCodes atomic.Pointer[map[int]string]

func activeEventCodes() map[int]string {
	if p := currentEventCodes.Load(); p != nil {
		return *p
	}
	return defaultCodeMap()
}

// DefaultMappingEntries seeds the editable table: one unit-wide row per known
// device event, with the device name carried in Description so the admin can tell
// which numeric code is which. Implements part of gateway.MappingProvider.
func DefaultMappingEntries() []mapping.Entry {
	entries := make([]mapping.Entry, 0, len(defaultCathexisEvents))
	for _, e := range defaultCathexisEvents {
		entries = append(entries, mapping.Entry{
			MapType:     mapTypeEvent,
			Code:        e.code,
			EventCode:   e.event,
			Description: e.name,
		})
	}
	return entries
}

// ApplyMappings overlays the admin-edited rows (unit-wide, model "") on top of
// the built-in defaults, so every known code is always resolvable even if the
// loaded set is partial. Implements part of gateway.MappingProvider.
func ApplyMappings(byModel mapping.ByModel) {
	chosen := defaultCodeMap()
	if t, ok := byModel[""]; ok {
		if m, ok := t[mapTypeEvent]; ok {
			for k, v := range m {
				if v != "" {
					chosen[k] = v
				}
			}
		}
	}
	currentEventCodes.Store(&chosen)
}

// sanitizeEventKey normalizes a raw event name for lookup (lowercase, non
// [a-z0-9:_-] runs → "_", collapse ":" runs, trim "_"). Mirrors the old gateway.
func sanitizeEventKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	out := b.String()
	for strings.Contains(out, "::") {
		out = strings.ReplaceAll(out, "::", ":")
	}
	return strings.Trim(out, "_")
}

// toStandardEventCodes derives the standard event-code list from a device payload.
// It reads payload.event (array or string) and payload.name, resolves each via
// the active (admin-editable) map (device name → synthetic code → event_code),
// falling back to "ALARM" for unknown names, and dedupes. An event message with
// nothing recognizable yields ["ALARM"].
func toStandardEventCodes(payload map[string]any, isEvent bool) []any {
	var raw []string
	switch ev := payload["event"].(type) {
	case []any:
		for _, e := range ev {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				raw = append(raw, s)
			}
		}
	case string:
		if strings.TrimSpace(ev) != "" {
			raw = append(raw, ev)
		}
	}
	if name, ok := payload["name"].(string); ok && strings.TrimSpace(name) != "" && name != "gps" {
		raw = append(raw, name)
	}
	if isEvent && len(raw) == 0 {
		raw = append(raw, "alarm")
	}

	active := activeEventCodes()
	seen := map[string]bool{}
	out := []any{}
	for _, name := range raw {
		key := sanitizeEventKey(name)
		if key == "" {
			continue
		}
		code := "ALARM"
		if syn, ok := nameToCode[key]; ok {
			if ev := active[syn]; ev != "" {
				code = ev
			}
		}
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}
