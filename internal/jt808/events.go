package jt808

import (
	"sort"
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// Event mapping for the N62: raw JT808 alarm bits and ULV/vendor TLVs are
// translated into ACM Standard Event Codes. These are the BUILT-IN DEFAULTS;
// at runtime they are overridable from the database (MappingProvider) so the
// still-provisional vendor subtypes (see docs/jt808/N62_EVENT_MAPPING.md) can be
// pinned down from live traffic without a redeploy. Defaults remain the fallback.

// Map-type identifiers — the persisted/editable grouping keys.
const (
	mapTypeAlarm    = "alarm"     // alarm-bitmask bit index -> code
	mapTypeAdas     = "adas"      // ULV TLV 0x64 subtype -> code
	mapTypeDms      = "dms"       // ULV TLV 0x65 subtype -> code
	mapTypeBsd      = "bsd"       // ULV TLV 0x67 subtype -> code
	mapTypeVendorE1 = "vendor_e1" // vendor TLV 0xE1 value -> code
	mapTypeVendor70 = "vendor_70" // vendor TLV 0x70 last-3-bytes (u24) -> code
)

// defaultAlarmEventMap maps the 0x0200 alarm-DWORD bit index to a code (JT808
// standard alarm bits; docs/jt808/N62_EVENT_MAPPING.md confirmed entries).
var defaultAlarmEventMap = map[int]string{
	0:  "PANIC",                 // emergency
	1:  "SPEEDING",              // overspeed
	14: "ALARM",                 // fatigue driving warning
	18: "SPEEDING",              // accumulated overspeed time
	19: "PARKING:START",         // timeout parking
	20: "ZONE:UNKNOWN_EVENT",    // enter/exit area
	21: "ZONE:UNKNOWN_EVENT",    // enter/exit route
	22: "ZONE:UNKNOWN_EVENT",    // route time abnormal
	23: "ZONE:UNKNOWN_EVENT",    // off track
	26: "ALARM",                 // vehicle stolen
	27: "ALARM:TAMPERING",       // illegal ignition
	28: "MOVEMENT:UNAUTHORIZED", // illegal displacement
	29: "COLLISION",             // collision warning
	30: "COLLISION:TURN_OVER",   // rollover warning
	31: "ALARM:DOOR_OPEN",       // illegal door open
}

// defaultAdasEventMap maps the ULV ADAS alarm (TLV 0x64) subtype byte.
var defaultAdasEventMap = map[int]string{
	0x01: "COLLISION",                    // forward collision
	0x02: "ALARM",                        // lane departure
	0x03: "FOLLOWING:DISTANCE:VIOLATION", // following distance
	0x04: "COLLISION",                    // pedestrian collision
}

// defaultDmsEventMap maps the ULV DMS alarm (TLV 0x65) subtype byte.
var defaultDmsEventMap = map[int]string{
	0x01: "AI:FATIGUE",
	0x02: "AI:CELLPHONE",
	0x03: "ALARM", // smoking
	0x04: "AI:DISTRACTION",
	0x05: "ALARM", // driver abnormal
	0x06: "AI:SEATBELT",
	0x0a: "AI:TAMPER", // camera occlusion
	0x11: "ALARM",     // driver change
	0x1f: "AI:TAMPER", // infrared blocking
}

// defaultBsdEventMap maps the ULV BSD alarm (TLV 0x67) subtype byte.
var defaultBsdEventMap = map[int]string{
	0x01: "ALARM", // rear approach
	0x02: "ALARM", // left rear
	0x03: "ALARM", // right rear
}

// defaultVendorE1EventMap maps the vendor TLV 0xE1 value byte
// (docs/jt808/N62_EVENT_MAPPING.md: 1/3 confirmed, 2/4 provisional).
var defaultVendorE1EventMap = map[int]string{
	0x01: "HARSH:ACCELERATION",
	0x02: "HARSH:BRAKING",
	0x03: "HARSH:CORNERING",
	0x04: "COLLISION",
}

// defaultVendor70EventMap maps the vendor TLV 0x70 last-3-bytes (as a u24) to a
// code (docs/jt808/N62_EVENT_MAPPING.md confirmed suffixes).
var defaultVendor70EventMap = map[int]string{
	0x020200: "IGNITION:ON",
	0x040100: "HARSH:CORNERING",
	0x050200: "COLLISION",
}

// Mappings is the active set of code→event lookup tables, swapped atomically by
// ApplyMappings.
type Mappings struct {
	Alarm    map[int]string
	Adas     map[int]string
	Dms      map[int]string
	Bsd      map[int]string
	VendorE1 map[int]string
	Vendor70 map[int]string
}

// currentMappingsByModel is the active per-model mapping set. The empty-model key
// ("") is the unit-wide default (the N62 is the only model today).
var currentMappingsByModel atomic.Pointer[map[string]*Mappings]

func init() {
	m := map[string]*Mappings{"": defaultMappings()}
	currentMappingsByModel.Store(&m)
}

func defaultMappings() *Mappings {
	return &Mappings{
		Alarm:    defaultAlarmEventMap,
		Adas:     defaultAdasEventMap,
		Dms:      defaultDmsEventMap,
		Bsd:      defaultBsdEventMap,
		VendorE1: defaultVendorE1EventMap,
		Vendor70: defaultVendor70EventMap,
	}
}

// mappingsForModel returns the active mapping set for a device model: its own
// table if present, else the unit default (""), else the built-in defaults.
func mappingsForModel(model string) *Mappings {
	if p := currentMappingsByModel.Load(); p != nil {
		if m, ok := (*p)[model]; ok && m != nil {
			return m
		}
		if m, ok := (*p)[""]; ok && m != nil {
			return m
		}
	}
	return defaultMappings()
}

// DefaultMappingEntries flattens the built-in defaults for seeding the database,
// in a stable (map_type, code) order.
func DefaultMappingEntries() []mapping.Entry {
	groups := []struct {
		mapType string
		m       map[int]string
	}{
		{mapTypeAlarm, defaultAlarmEventMap},
		{mapTypeAdas, defaultAdasEventMap},
		{mapTypeDms, defaultDmsEventMap},
		{mapTypeBsd, defaultBsdEventMap},
		{mapTypeVendorE1, defaultVendorE1EventMap},
		{mapTypeVendor70, defaultVendor70EventMap},
	}
	var entries []mapping.Entry
	for _, g := range groups {
		codes := make([]int, 0, len(g.m))
		for code := range g.m {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		for _, code := range codes {
			entries = append(entries, mapping.Entry{MapType: g.mapType, Code: code, EventCode: g.m[code]})
		}
	}
	return entries
}

// buildMappings builds a Mappings from one loaded table, keeping the built-in
// default for any map_type missing or empty (so a partial table never wipes out
// mappings).
func buildMappings(loaded mapping.Table) *Mappings {
	d := defaultMappings()
	pick := func(mt string, def map[int]string) map[int]string {
		if loaded != nil {
			if m, ok := loaded[mt]; ok && len(m) > 0 {
				return m
			}
		}
		return def
	}
	return &Mappings{
		Alarm:    pick(mapTypeAlarm, d.Alarm),
		Adas:     pick(mapTypeAdas, d.Adas),
		Dms:      pick(mapTypeDms, d.Dms),
		Bsd:      pick(mapTypeBsd, d.Bsd),
		VendorE1: pick(mapTypeVendorE1, d.VendorE1),
		Vendor70: pick(mapTypeVendor70, d.Vendor70),
	}
}

// ApplyMappings installs the loaded per-model mapping tables as the active set.
// Pass nil to reset to the built-in defaults.
func ApplyMappings(byModel mapping.ByModel) {
	out := map[string]*Mappings{}
	for model, table := range byModel {
		out[model] = buildMappings(table)
	}
	if _, ok := out[""]; !ok {
		out[""] = defaultMappings()
	}
	currentMappingsByModel.Store(&out)
}

// Trace sources for the live mapping test (mirrors the Howen/Cathexis vocabulary).
const (
	traceTable    = "table"    // a mapping row (DB override or built-in default) resolved it
	traceFallback = "fallback" // a raw alarm signal was present but no row maps its code
)

// mapTraceEntry records how one raw JT808 alarm signal decoded against the active
// mapping tables, for the live mapping-test mode. It is logged (info) alongside
// event_forward and never affects the universal message sent to sinks. The JSON
// field names match what the admin Mapping Test reads.
type mapTraceEntry struct {
	MapType   string `json:"map_type"`   // "alarm" | "adas" | "dms" | "bsd" | "vendor_e1" | "vendor_70"
	Code      int    `json:"code"`       // bit index (alarm) or subtype/value looked up in that table
	EventCode string `json:"event_code"` // resolved standard code ("" when unmapped)
	Source    string `json:"source"`     // traceTable on a hit, traceFallback on a miss
}

// resolveEvents maps a decoded location's alarm bits and vendor/ULV TLVs to a
// de-duplicated, stable-ordered list of ACM Standard Event Codes. An empty result
// means a plain GPS update with no event signal.
func resolveEvents(loc location, model string) []string {
	events, _ := resolveEventsTrace(loc, model)
	return events
}

// resolveEventsTrace is resolveEvents plus a per-signal decode trace. The event
// list is identical to resolveEvents; the trace records every raw alarm signal that
// was present (mapped or not) so the mapping test can flag unmapped codes. A trace
// entry exists even when nothing maps, which is how a wholly-unmapped alarm still
// surfaces in the tester.
func resolveEventsTrace(loc location, model string) ([]string, []mapTraceEntry) {
	m := mappingsForModel(model)
	var out []string
	var trace []mapTraceEntry
	seen := map[string]bool{}
	add := func(code string) {
		if code == "" || seen[code] {
			return
		}
		seen[code] = true
		out = append(out, code)
	}
	// rec resolves one raw signal against its table: records a trace entry (table on
	// a hit, fallback on a miss) and adds the mapped event.
	rec := func(mapType string, code int, table map[int]string) {
		ev := table[code]
		src := traceFallback
		if ev != "" {
			src = traceTable
		}
		add(ev)
		trace = append(trace, mapTraceEntry{MapType: mapType, Code: code, EventCode: ev, Source: src})
	}

	// Alarm bitmask (low bit first, for stable ordering).
	for bit := 0; bit < 32; bit++ {
		if loc.Alarm&(1<<uint(bit)) != 0 {
			rec(mapTypeAlarm, bit, m.Alarm)
		}
	}
	// ULV ADAS/DMS/BSD: subtype is byte index 4 of the TLV value (after the 4-byte
	// alarm id), per the ULV spec.
	subtype := func(v []byte) (int, bool) {
		if len(v) >= 5 {
			return int(v[4]), true
		}
		return 0, false
	}
	if v, ok := loc.TLVs[0x64]; ok {
		if st, ok := subtype(v); ok {
			rec(mapTypeAdas, st, m.Adas)
		}
	}
	if v, ok := loc.TLVs[0x65]; ok {
		if st, ok := subtype(v); ok {
			rec(mapTypeDms, st, m.Dms)
		}
	}
	if v, ok := loc.TLVs[0x67]; ok {
		if st, ok := subtype(v); ok {
			rec(mapTypeBsd, st, m.Bsd)
		}
	}
	// Vendor 0xE1: first value byte.
	if v, ok := loc.TLVs[0xe1]; ok && len(v) >= 1 {
		rec(mapTypeVendorE1, int(v[0]), m.VendorE1)
	}
	// Vendor 0x70: last 3 bytes as a u24.
	if v, ok := loc.TLVs[0x70]; ok && len(v) >= 3 {
		tail := v[len(v)-3:]
		key := int(tail[0])<<16 | int(tail[1])<<8 | int(tail[2])
		rec(mapTypeVendor70, key, m.Vendor70)
	}
	return out, trace
}
