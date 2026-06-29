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

// The device names below are the actual event-message `name` values an MVR5 unit
// emits (MVR5 Third-Party API §4.1 "Supported Events", cross-checked against a
// live unit's own `eventpreviews` config list). Targets are the canonical ACM
// Standard Event Codes (several tagged "Cathexis Only" / "Cathexis + Howen").
//
// Codes 1–6 were correct from the start; 7–14 (collision_warning, over_speed,
// overspeeding, panic, idling_start, idling_end, …) used names the firmware never
// actually sends, so before this table every real event except the harsh ones
// fell through to "ALARM". They are kept (codes are append-only) as harmless,
// admin-editable rows; codes 15+ are the real names the device sends.
var defaultCathexisEvents = []cathexisEvent{
	{1, "ignition_on", "IGNITION:ON"},
	{2, "ignition_off", "IGNITION:OFF"},
	{3, "harsh_braking", "HARSH:BRAKING"},
	{4, "harsh_acceleration", "HARSH:ACCELERATION"},
	{5, "harsh_turning", "HARSH:CORNERING"},
	{6, "harsh_impact", "COLLISION"},
	// 7–14: legacy/aliased names the firmware does not actually emit. Kept for
	// append-only stability; superseded by the real names below.
	{7, "collision_warning", "COLLISION"},
	{8, "rollover_warning", "COLLISION:TURN_OVER"},
	{9, "over_speed", "SPEEDING"},
	{10, "over_speed_warning", "SPEEDING"},
	{11, "overspeeding", "SPEEDING"},
	{12, "panic", "PANIC"},
	{13, "idling_start", "IDLING:START"},
	{14, "idling_end", "IDLING:END"},
	// 15+: the real event names an MVR5 emits (API §4.1).
	{15, "idle_starts", "IDLING:START"},
	{16, "idle_stops", "IDLING:END"},
	{17, "idle_periodic", "IDLING:PERIODIC"},
	{18, "speeding", "SPEEDING"},
	{19, "gps_lock", "GPS:LOCKED"},
	{20, "gps_lost", "GPS:TIMEOUT"},
	{21, "button_pressed", "PANIC"},
	{22, "button_released", "PANIC"},
	// AI / DMS driver-monitoring events.
	{23, "tamper", "AI:TAMPER"},
	{24, "fatigue", "AI:FATIGUE"},
	{25, "distraction", "AI:DISTRACTION"},
	{26, "seatbelt", "AI:SEATBELT"},
	{27, "yawn", "AI:YAWN"},
	{28, "cellphone", "AI:CELLPHONE"},
	{29, "passenger", "AI:PASSENGER"},
	{30, "smoking", "AI:SMOKING"},
	{31, "followingdistance", "FOLLOWING:DISTANCE:VIOLATION"},
	// Telephony.
	{32, "call_started", "CALL:STARTED"},
	{33, "call_ended", "CALL:ENDED"},
	// Vehicle / power.
	{34, "motion_start", "TRIP:START"},
	{35, "power_loss", "BATTERY:DISCONNECTED"},
	// Power-state lifecycle (also drives the device online/sleep state — see
	// reconcileLifecycle in server.go).
	{36, "deep_sleep", "SLEEP"},
	{37, "entered_standby", "STANDBY"},
	{38, "wake_dapi_on", "STANDBY:WAKE:DAPI"},
	{39, "wake_dapi_off", "STANDBY:ENTER:DAPI"},
	{40, "wake_imu_on", "STANDBY:WAKE:IMU"},
	{41, "wake_imu_off", "STANDBY:ENTER:IMU"},
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
