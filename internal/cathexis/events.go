package cathexis

import "strings"

// standardEventCodes maps Cathexis event names to ACM standard event codes.
// Ported from dfm-mvr-gateway src/tcp/controlServer.js STANDARD_EVENT_CODE_MAP.
// Cathexis events are string-named (not int codes), so this lives in code rather
// than the admin-editable int-keyed mapping table.
var standardEventCodes = map[string]string{
	"ignition_on":        "IGNITION:ON",
	"ignition_off":       "IGNITION:OFF",
	"harsh_braking":      "HARSH:BRAKING",
	"harsh_acceleration": "HARSH:ACCELERATION",
	"harsh_turning":      "HARSH:CORNERING",
	"harsh_impact":       "COLLISION",
	"collision_warning":  "COLLISION",
	"rollover_warning":   "COLLISION:TURN_OVER",
	"over_speed":         "SPEEDING",
	"over_speed_warning":  "SPEEDING",
	"overspeeding":       "SPEEDING",
	"panic":              "PANIC",
	"idling_start":       "IDLING:START",
	"idling_end":         "IDLING:END",
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
// It reads payload.event (array or string) and payload.name (the event type), maps
// each via standardEventCodes (falling back to "ALARM"), and dedupes. For an event
// message with nothing recognizable it yields ["ALARM"].
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

	seen := map[string]bool{}
	out := []any{}
	for _, name := range raw {
		key := sanitizeEventKey(name)
		if key == "" {
			continue
		}
		code := standardEventCodes[key]
		if code == "" {
			code = standardEventCodes[strings.ReplaceAll(key, ":", "_")]
		}
		if code == "" {
			code = "ALARM"
		}
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}
