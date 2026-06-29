// Navtelecom event handling: the FLEX "event id" (telemetry field 2) → ACM
// Standard Event Code mapping.
//
// The numeric event ids come from Navtelecom's "Protocol NTCB. Table of
// telematics events codes equivalence" (the standard codes a Signal/Smart device
// emits, e.g. 4688 IGN_ON, 5897 GPS_GO, 5904 AINF_GZIN). defaultEventCodes maps
// the common ones to ACM Standard Event Codes; the full table is large and
// install-specific, so the mapping is also editable from the admin Device
// Mapping screen (map type "event_code") and applied live without a redeploy.
// Codes not in the default and not added by the admin pass through as a raw
// "NTC:<code>" label so events are never lost and operators can discover which
// codes to map.
//
// Two id classes are routine, not events, and forward as plain GPS: id 0xFF00
// (the "current state" id on ~C ping-with-data telemetry) and the timer family
// (periodic black-box recording timers) in routineEventCodes.
package navtelecom

import (
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// mapTypeEventCode is the editable map-type key: raw FLEX event id → ACM code.
const mapTypeEventCode = "event_code"

// defaultEventCodes maps the common Navtelecom standard event ids (decimal, FLEX
// field 2) to ACM Standard Event Codes. It is the seeded, admin-editable default;
// the source mnemonics are in eventCodeDescriptions. Install-specific codes
// (digital inputs, CAN, EcoDriving, key fobs, temperature sensors, …) are left to
// pass through / be mapped per fleet from the admin.
var defaultEventCodes = map[int]string{
	4513: "ALARM:VIBRATION",   // SH1_Y  soft impact sensor triggered
	4529: "ALARM:VIBRATION",   // SH2_Y  strong impact sensor triggered
	4656: "BATTERY:LOW:EXT",   // AG_DOWN main supply below threshold
	4657: "BATTERY:OK:EXT",    // AG_NORM main supply recovered
	4672: "BATTERY:LOW:INT",   // AR_DOWN backup battery below threshold
	4673: "BATTERY:OK:INT",    // AR_NORM backup battery recovered
	4688: "IGNITION:ON",       // IGN_ON  ignition on (by power supply)
	4689: "IGNITION:OFF",      // IGN_OFF ignition off (by power supply)
	5376: "POWER:ON",          // START   device turning on
	5380: "PANIC",             // EVNT_ALARM_GLOBAL  alarm
	5894: "SPEED:LOW",         // GPS_VMIN speed below minimum
	5895: "SPEEDING:START",    // GPS_VMAX speed above maximum
	5897: "MOVEMENT:DETECTED", // GPS_GO   object has moved
	5898: "PARKING:START",     // GPS_STOP object has stopped
	5900: "TOWING:START",      // EVAC     evacuation (vehicle being towed)
	5901: "TOWING:END",        // EVAC_END evacuation ended
	5904: "ZONE:ENTER",        // AINF_GZIN  entered geofence
	5905: "ZONE:EXIT",         // AINF_GZOUT left geofence
	5906: "SPEEDING:START",    // SM_MAX   driving mode: speeding
	5907: "SPEEDING:END",      // SM_NORM  driving mode: speed normal
}

// eventCodeDescriptions carries the Navtelecom source mnemonic for each default
// code, surfaced as the seeded row's Description so the admin shows what each
// raw id means.
var eventCodeDescriptions = map[int]string{
	4513: "SH1_Y soft impact", 4529: "SH2_Y strong impact",
	4656: "AG_DOWN main supply low", 4657: "AG_NORM main supply ok",
	4672: "AR_DOWN backup battery low", 4673: "AR_NORM backup battery ok",
	4688: "IGN_ON", 4689: "IGN_OFF", 5376: "START device power-on",
	5380: "EVNT_ALARM_GLOBAL", 5894: "GPS_VMIN", 5895: "GPS_VMAX",
	5897: "GPS_GO moved", 5898: "GPS_STOP stopped", 5900: "EVAC towed",
	5901: "EVAC_END", 5904: "AINF_GZIN geofence in", 5905: "AINF_GZOUT geofence out",
	5906: "SM_MAX speeding", 5907: "SM_NORM speed normal",
}

// routineEventCodes are periodic/black-box recording timer ids that carry routine
// telemetry, not a real event: they forward as plain GPS (like the 0xFF00
// current-state id). An admin mapping for one of these still takes precedence.
var routineEventCodes = map[uint16]bool{
	5634: true, // TMR_B_G_0 telemetry record timer (normal mode)
	5635: true, // TMR_B_G_1 telemetry record timer (protection mode)
	5636: true, // TMR_GPRS  send-to-server timer
	5899: true, // GPS_TIMER telemetry recording timer from last current event
}

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
// The admin can edit or extend these rows (and add install-specific codes).
func DefaultMappingEntries() []mapping.Entry {
	codes := make([]int, 0, len(defaultEventCodes))
	for c := range defaultEventCodes {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	entries := make([]mapping.Entry, 0, len(codes))
	for _, c := range codes {
		entries = append(entries, mapping.Entry{
			MapType:     mapTypeEventCode,
			Code:        c,
			EventCode:   defaultEventCodes[c],
			Description: eventCodeDescriptions[c],
		})
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
// message, returning ("", false) when the record is routine telemetry (no event).
// Order: the reserved current-state id is routine; an explicit (admin or default)
// mapping wins next; the timer family is routine; anything else passes through as
// "NTC:<code>" so unknown events are never silently dropped.
func eventForCode(eventID uint16) (string, bool) {
	if eventID == eventCurrentState {
		return "", false
	}
	if code, ok := activeEventCodes()[int(eventID)]; ok && code != "" {
		return code, true
	}
	if routineEventCodes[eventID] {
		return "", false
	}
	return fmt.Sprintf("NTC:%d", eventID), true
}
