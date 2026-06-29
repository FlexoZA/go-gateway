// Navtelecom event handling: the FLEX "event id" (telemetry field 2) → ACM
// Standard Event Code mapping.
//
// Navtelecom assigns event codes in the device's OWN configuration (the protocol
// does not fix them), so there is no universal built-in table. The mapping is
// therefore editable from the admin Device Mapping screen (map type
// "event_code") and applied to the running gateway without a redeploy. Until a
// code is mapped, its events are still forwarded under a raw "NTC:<code>" label
// so they are visible and the operator can discover which codes to map.
//
// Event id 0xFF00 is the reserved "current state" id carried by routine ~C
// (ping-with-data) telemetry and never produces an event.
package navtelecom

import (
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// mapTypeEventCode is the editable map-type key: raw FLEX event id → ACM code.
const mapTypeEventCode = "event_code"

// defaultEventCodes is the built-in default raw-event-id → ACM-code table. It is
// intentionally empty: Navtelecom codes are fleet/config-specific, so the table
// is populated from the admin rather than shipped. See the package doc above.
var defaultEventCodes = map[int]string{}

// currentEventCodes is the active map, swapped atomically by ApplyMappings.
var currentEventCodes atomic.Pointer[map[int]string]

func init() { currentEventCodes.Store(cloneIntStr(defaultEventCodes)) }

func activeEventCodes() map[int]string {
	if p := currentEventCodes.Load(); p != nil {
		return *p
	}
	return defaultEventCodes
}

func cloneIntStr(m map[int]string) *map[int]string {
	cp := make(map[int]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &cp
}

// DefaultMappingEntries flattens the built-in default map for seeding the
// database, in stable code order. Implements part of gateway.MappingProvider.
// (Empty by default — Navtelecom codes are added from the admin.)
func DefaultMappingEntries() []mapping.Entry {
	codes := make([]int, 0, len(defaultEventCodes))
	for c := range defaultEventCodes {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	entries := make([]mapping.Entry, 0, len(codes))
	for _, c := range codes {
		entries = append(entries, mapping.Entry{MapType: mapTypeEventCode, Code: c, EventCode: defaultEventCodes[c]})
	}
	return entries
}

// ApplyMappings installs the loaded event-code map as the active set, keeping the
// built-in default when the table lacks (or empties) the map type. Navtelecom is
// a single-model unit, so it uses the unit default ("") table and ignores any
// per-model tables. Pass nil to reset to the built-in default.
func ApplyMappings(byModel mapping.ByModel) {
	chosen := defaultEventCodes
	if t, ok := byModel[""]; ok {
		if m, ok := t[mapTypeEventCode]; ok && len(m) > 0 {
			chosen = m
		}
	}
	currentEventCodes.Store(cloneIntStr(chosen))
}

// eventForCode resolves a raw FLEX event id to an event label for the universal
// message. It returns ("", false) for the reserved current-state id (routine ~C
// telemetry, no event). Otherwise it returns the mapped ACM code, or a raw
// "NTC:<code>" passthrough when the code is not yet mapped, so events are never
// silently dropped.
func eventForCode(eventID uint16) (string, bool) {
	if eventID == eventCurrentState {
		return "", false
	}
	if code, ok := activeEventCodes()[int(eventID)]; ok && code != "" {
		return code, true
	}
	return fmt.Sprintf("NTC:%d", eventID), true
}
