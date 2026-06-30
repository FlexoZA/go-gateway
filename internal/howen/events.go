package howen

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// Event-code maps ported verbatim from howenCodec.js. These translate raw Howen
// alarm codes/subtypes into ACM Standard Event Codes before they reach the
// universal message. They are the BUILT-IN DEFAULTS: at runtime they can be
// overridden from the database (see Mappings / ApplyMappings) so a front end can
// edit them without a redeploy. The defaults remain the fallback.

// Map-type identifiers — the persisted/editable grouping keys.
const (
	mapTypeInput              = "input"
	mapTypeVibrationDirection = "vibration_direction"
	mapTypeGeofenceStatus     = "geofence_status"
	mapTypeVoltage            = "voltage"
	mapTypeDmsAdas            = "dms_adas"
	mapTypeMediaAlarmSubtype  = "media_alarm_subtype"
	mapTypeEventCode          = "event_code"
)

// defaultInputEventMap maps the input-trigger sub-field `num` (spec §2 / §3.8).
// 14/15/16 are door-CLOSE signals (previously mis-mapped to ALARM:DOOR_OPEN);
// 9/10 are turn signals and 12 is reverse (previously flattened to ALARM).
var defaultInputEventMap = map[int]string{
	1: "PANIC", 2: "ALARM:DOOR_OPEN", 3: "ALARM:DOOR_OPEN", 4: "ALARM:DOOR_OPEN",
	5: "INPUT:LOW_BEAM", 6: "INPUT:HIGH_BEAM", 9: "INPUT:TURN_RIGHT", 10: "INPUT:TURN_LEFT",
	11: "ALARM:FOOTBRAKE", 12: "INPUT:REVERSE",
	14: "INPUT:DOOR_CLOSE", 15: "INPUT:DOOR_CLOSE", 16: "INPUT:DOOR_CLOSE",
	17: "INPUT:INTERCOM", 18: "INPUT:LIFT", 22: "ALARM", 23: "INPUT:SAFE_TO_LOAD",
}

var defaultVibrationDirectionEventMap = map[int]string{
	1: "ALARM:VIBRATION", 2: "ALARM:VIBRATION", 3: "ALARM:VIBRATION",
	4: "COLLISION", 5: "COLLISION:TURN_OVER", 6: "HARSH:CORNERING",
	7: "HARSH:ACCELERATION", 8: "HARSH:BRAKING",
}

// defaultGeofenceStatusEventMap maps the geofence sub-field `st` (spec §7).
// 4/5 are low-speed alarm/warning (previously ZONE:UNKNOWN_EVENT); 9/10 are
// pre-entry/pre-exit (previously conflated with ENTER/EXIT).
var defaultGeofenceStatusEventMap = map[int]string{
	0: "ZONE:ENTER", 1: "ZONE:EXIT", 2: "ZONE:SPEEDING", 3: "ZONE:SPEEDING:WARNING",
	4: "ZONE:SPEED_LOW", 5: "ZONE:SPEED_LOW:WARNING", 6: "ZONE:FORBIDDEN_STOP",
	7: "ZONE:FORBIDDEN_STOP", 8: "ZONE:DELAYED_STOP", 9: "ZONE:PRE_ENTER", 10: "ZONE:PRE_EXIT",
}

// defaultVoltageEventMap maps the voltage sub-field `dt` (spec §14).
// 2 is high-voltage (not generic abnormal); 6 is abnormal shutdown; 7 is
// start-up/power-on (previously mis-mapped to BATTERY:ABNORMAL:EXT).
var defaultVoltageEventMap = map[int]string{
	1: "BATTERY:LOW:EXT", 2: "BATTERY:HIGH:EXT", 3: "BATTERY:DISCONNECTED",
	4: "BATTERY:CONNECTED", 5: "BATTERY:DISCONNECTED", 6: "POWER:OFF:ABNORMAL",
	7: "POWER:ON",
}

var defaultDmsAdasEventMap = map[int]string{
	1: "COLLISION", 2: "AI:LANE_DEPARTURE", 3: "FOLLOWING:DISTANCE:VIOLATION",
	4: "COLLISION", 5: "HARSH:DRIVING", 6: "AI:TRAFFIC_SIGN:VIOLATION",
	7: "HARSH:ACCELERATION", 8: "HARSH:BRAKING", 9: "FOLLOWING:DISTANCE:VIOLATION",
	16: "COLLISION", 17: "COLLISION", 18: "FOLLOWING:DISTANCE:VIOLATION",
	19: "AI:LANE_DEPARTURE", 20: "AI:LANE_DEPARTURE", 21: "COLLISION",
	33: "AI:FATIGUE", 34: "AI:CELLPHONE", 35: "AI:SMOKING", 36: "AI:DISTRACTION",
	37: "AI:DRIVER:ABNORMAL", 39: "AI:EYES_CLOSED:CRITICAL", 40: "AI:YAWN:CRITICAL",
	41: "AI:CODRIVER", 49: "AI:DRIVER:CHANGE", 65: "AI:EYES_CLOSED", 66: "AI:YAWN",
	67: "AI:LENS_COVERED", 68: "AI:DISTRACTION", 69: "AI:SEATBELT", 70: "AI:NO_DRIVER",
	71: "AI:DRINKING", 72: "AI:DRIVER:CHANGE", 73: "AI:DRIVER:RETURN",
	80: "AI:EYES_DETECTION_FAILED", 81: "AI:AUTH:OK", 82: "AI:AUTH:FAIL", 83: "AI:NO_DRIVER",
	85: "AI:DRIVER:MASK", 87: "AI:EATING", 96: "AI:BLINDSPOT", 97: "AI:BLINDSPOT", 98: "AI:BLINDSPOT",
	99: "AI:BLINDSPOT", 100: "AI:BLINDSPOT", 101: "AI:BLINDSPOT", 102: "AI:BLINDSPOT",
	103: "AI:BLINDSPOT", 104: "AI:BLINDSPOT", 105: "AI:BLINDSPOT", 106: "AI:BLINDSPOT",
	107: "AI:BLINDSPOT",
}

var defaultMediaAlarmSubtypeEventMap = map[int]string{
	2: "AI:LANE_DEPARTURE", 18: "FOLLOWING:DISTANCE:VIOLATION", 35: "AI:SMOKING",
	39: "AI:EYES_CLOSED:CRITICAL", 40: "AI:YAWN:CRITICAL", 65: "AI:EYES_CLOSED",
	66: "AI:YAWN", 67: "AI:LENS_COVERED", 68: "AI:DISTRACTION", 69: "AI:SEATBELT",
	70: "AI:NO_DRIVER", 80: "AI:EYES_DETECTION_FAILED",
}

// defaultEventCodeMap maps the main alarm code `ec` for codes not resolved by
// built-in switch logic. Codes/labels follow the official online master list
// (docs/Howen_master_event_codes_2026.csv); see docs/Howen_mapping_improvements.md.
// ec 13 (geofence), 15 (door) and 22 (swipe-card) are resolved by switch logic
// in mapHowenEventCodes, so they are bypassed here (not editable event_code rows).
var defaultEventCodeMap = map[int]string{
	1: "VIDEO_LOSS", 2: "ALARM:MOTION", 3: "ALARM:TAMPERING", 4: "ALARM", 5: "PANIC",
	6: "SPEED:LOW", 7: "SPEEDING", 8: "TEMP:LOW", 9: "TEMP:HIGH", 11: "PARKING:START",
	12: "ALARM:VIBRATION", 16: "ALARM:HARDWARE:FAULT", 19: "IGNITION:OFF",
	24: "HARSH:ACCELERATION", 25: "HARSH:BRAKING", 26: "SPEED:LOW",
	27: "SPEEDING", 28: "ALARM", 29: "PEOPLE:COUNT", 30: "ALARM", 31: "IGNITION:ON", 32: "IDLING:START",
	33: "GPS:ANTENNA:BREAK", 34: "GPS:ANTENNA:SHORT", 36: "CANBUS:ABNORMAL", 37: "TOWING:START",
	40: "VEHICLE:MOVE", 41: "TRIP:START", 43: "TRIP:END", 44: "GPS:RECOVER", 48: "SPEEDING",
	49: "LOAD:ALARM", 50: "SIM:LOST", 59: "DEVICE:WAKEUP", 60: "SATELLITE:ABNORMAL",
	61: "ALARM:ALCOHOL", 62: "AI:FATIGUE", 76: "GPS:JAMMED", 78: "AI:INSTALL:ABNORMAL",
	80: "AI:CALIBRATION", 768: "TRIP:UNKNOWN", 769: "TIRE:PRESSURE", 770: "ALARM:HARDWARE:FAULT",
	772: "DEVICE:SELF_CHECK", 773: "WATER:FLOW",
	// ec 771 (datahub/OBD) is intercepted in handleAlarmData and forwarded as a
	// "gps" telemetry message (see buildDatahubPayload), so it has no row here.
}

// Mappings is the active set of code→event lookup tables used by
// mapHowenEventCodes. It is swapped atomically by ApplyMappings.
type Mappings struct {
	Input              map[int]string
	VibrationDirection map[int]string
	GeofenceStatus     map[int]string
	Voltage            map[int]string
	DmsAdas            map[int]string
	MediaAlarmSubtype  map[int]string
	EventCode          map[int]string
}

// currentMappingsByModel is the active per-model mapping sets, keyed by device
// model. The empty-model key ("") is the unit-wide default. Swapped atomically by
// ApplyMappings.
var currentMappingsByModel atomic.Pointer[map[string]*Mappings]

func init() {
	m := map[string]*Mappings{"": defaultMappings()}
	currentMappingsByModel.Store(&m)
}

// mappingsForModel returns the active mapping set for a device model: the model's
// own table if it has one, else the unit default (""), else the built-in defaults.
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

func defaultMappings() *Mappings {
	return &Mappings{
		Input:              defaultInputEventMap,
		VibrationDirection: defaultVibrationDirectionEventMap,
		GeofenceStatus:     defaultGeofenceStatusEventMap,
		Voltage:            defaultVoltageEventMap,
		DmsAdas:            defaultDmsAdasEventMap,
		MediaAlarmSubtype:  defaultMediaAlarmSubtypeEventMap,
		EventCode:          defaultEventCodeMap,
	}
}

// bypassedEventCodes are alarm codes that mapHowenEventCodes resolves with
// built-in logic and never looks up in the editable event_code map, so editing
// their event_code row would have no effect. They fall into two groups:
//   - literal / dynamic outputs: 1 (channel video-loss), 7/27/48 & 32
//     (SPEEDING/IDLING START-vs-END), 11/41/43/768 (parking/trip literals),
//     15 (door open/close from st), 22 (swipe-card from tp);
//   - sub-table codes 12/13/28/30, which read their OWN map_type
//     (vibration_direction/geofence_status/voltage/dms_adas), never event_code.
//
// These are not seeded as editable event_code rows, and any seeded by older
// builds are pruned (see PrunableMappings), so the admin only shows event_code
// rows that take effect. The sub-table map_types remain fully editable.
var bypassedEventCodes = []int{1, 7, 11, 12, 13, 15, 22, 27, 28, 30, 32, 41, 43, 48, 768}

func isBypassedEventCode(code int) bool {
	for _, c := range bypassedEventCodes {
		if c == code {
			return true
		}
	}
	return false
}

// PrunableEventCodeMappings reports the event_code rows the howen unit handles
// internally, for the runner to delete from databases seeded by older builds.
func PrunableEventCodeMappings() []mapping.Prune {
	return []mapping.Prune{{MapType: mapTypeEventCode, Codes: bypassedEventCodes}}
}

// DefaultMappingEntries flattens the built-in defaults for seeding the database.
// Entries are emitted in a stable (map_type, code) order. event_code rows for
// codes the switch resolves internally (bypassedEventCodes) are omitted — they
// would show as editable in the admin but have no effect.
func DefaultMappingEntries() []mapping.Entry {
	groups := []struct {
		mapType string
		m       map[int]string
	}{
		{mapTypeInput, defaultInputEventMap},
		{mapTypeVibrationDirection, defaultVibrationDirectionEventMap},
		{mapTypeGeofenceStatus, defaultGeofenceStatusEventMap},
		{mapTypeVoltage, defaultVoltageEventMap},
		{mapTypeDmsAdas, defaultDmsAdasEventMap},
		{mapTypeMediaAlarmSubtype, defaultMediaAlarmSubtypeEventMap},
		{mapTypeEventCode, defaultEventCodeMap},
	}
	var entries []mapping.Entry
	for _, g := range groups {
		codes := make([]int, 0, len(g.m))
		for code := range g.m {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		for _, code := range codes {
			if g.mapType == mapTypeEventCode && isBypassedEventCode(code) {
				continue // handled by built-in logic; not an editable row
			}
			entries = append(entries, mapping.Entry{MapType: g.mapType, Code: code, EventCode: g.m[code]})
		}
	}
	return entries
}

// buildMappings builds a Mappings from one loaded table, keeping the built-in
// default for any map_type missing or empty in the table (so a partial table can
// never wipe out mappings).
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
		Input:              pick(mapTypeInput, d.Input),
		VibrationDirection: pick(mapTypeVibrationDirection, d.VibrationDirection),
		GeofenceStatus:     pick(mapTypeGeofenceStatus, d.GeofenceStatus),
		Voltage:            pick(mapTypeVoltage, d.Voltage),
		DmsAdas:            pick(mapTypeDmsAdas, d.DmsAdas),
		MediaAlarmSubtype:  pick(mapTypeMediaAlarmSubtype, d.MediaAlarmSubtype),
		EventCode:          pick(mapTypeEventCode, d.EventCode),
	}
}

// ApplyMappings installs the loaded per-model mapping tables as the active set.
// The empty-model table ("") is the unit default; each model's table is a full
// table for that model (a model with no table falls back to the default). Pass nil
// to reset to the built-in defaults.
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

// numberOrNullInt parses a Howen detail value (string/number) into an int,
// returning (0,false) when absent or unparseable. Mirrors the JS numberOrNull
// used inside howenCodec.js (which accepts strings like "34").
func numberOrNullInt(v any) (int, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return int(t), true
	case int:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return int(f), true
	default:
		return 0, false
	}
}

// numberOrNullFloat parses a Howen detail value (string/number) into a float64,
// preserving fractional values (unlike numberOrNullInt). Returns (0,false) when
// absent or unparseable.
func numberOrNullFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func detailGet(detail map[string]any, keys ...string) any {
	if detail == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := detail[k]; ok {
			return v
		}
	}
	return nil
}

func mapHowenVideoLoss(detail map[string]any) string {
	if ch, ok := numberOrNullInt(detailGet(detail, "ch", "Ch")); ok {
		return "VIDEO:SIGNAL_LOSS:CHANNEL:" + strconv.Itoa(ch)
	}
	return "ALARM"
}

var mediaFolderRe = regexp.MustCompile(`^\d{6}_(\d+)$`)
var mediaFileRe = regexp.MustCompile(`^\d+_\d+_(\d+)_\d+_\d+\.[a-zA-Z0-9]+$`)

func parseHowenMediaAlarmSubtype(detail map[string]any) (int, bool) {
	fn, _ := detailGet(detail, "fn").(string)
	if fn == "" {
		return 0, false
	}
	parts := []string{}
	for _, p := range strings.Split(fn, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for _, p := range parts {
		if m := mediaFolderRe.FindStringSubmatch(p); m != nil {
			return numberOrNullInt(m[1])
		}
	}
	base := fn
	if len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	if m := mediaFileRe.FindStringSubmatch(base); m != nil {
		return numberOrNullInt(m[1])
	}
	return 0, false
}

// mapTraceSource classifies how one alarm resolved: it matched an editable
// mapping row ("table"), a hardcoded protocol rule ("builtin"), or nothing —
// it fell back to a generic event ("fallback"). The live mapping test mode
// highlights "fallback" entries so an operator can see device codes that have
// no mapping yet.
const (
	traceTable    = "table"
	traceBuiltin  = "builtin"
	traceFallback = "fallback"
)

// mapTraceEntry records how a single alarm was decoded against the mapping
// tables, for the live mapping test mode. It is logged (debug) alongside
// alarm_forward and never affects the universal message sent to sinks.
type mapTraceEntry struct {
	EC        int    `json:"ec"`                 // raw Howen event code
	MapType   string `json:"map_type,omitempty"` // mapping table consulted ("" = builtin rule)
	Code      int    `json:"code"`               // sub-field value looked up in that table (or the EC for builtins)
	EventCode string `json:"event_code"`         // resolved standard code ("" when unmapped)
	Source    string `json:"source"`             // traceTable | traceBuiltin | traceFallback
}

// mapHowenEventCodes ports howenCodec.js mapHowenEventCodes. `alarm` is the raw
// alarm JSON object (used for the `et` end-time check). It reads the active mapping
// set for the device's model (which may have been overridden from the database),
// falling back to the unit default then the built-in defaults.
func mapHowenEventCodes(model string, eventCode any, detail map[string]any, alarm map[string]any) []string {
	events, _ := mapHowenEventCodesTrace(model, eventCode, detail, alarm)
	return events
}

// mapHowenEventCodesTrace is mapHowenEventCodes plus a per-alarm decode trace.
// The event list it returns is byte-for-byte identical to mapHowenEventCodes
// (which is a thin wrapper); the trace is extra provenance for the test mode.
func mapHowenEventCodesTrace(model string, eventCode any, detail map[string]any, alarm map[string]any) ([]string, []mapTraceEntry) {
	code, ok := numberOrNullInt(eventCode)
	if !ok {
		return []string{"ALARM"}, []mapTraceEntry{{EventCode: "ALARM", Source: traceFallback}}
	}
	m := mappingsForModel(model)
	events := []string{}
	trace := []mapTraceEntry{}
	add := func(e string) {
		if e == "" {
			return
		}
		for _, existing := range events {
			if existing == e {
				return
			}
		}
		events = append(events, e)
	}
	// rec resolves a table-driven branch: adds the event (if any) and records a
	// trace entry tagged "table" on a hit or "fallback" on a miss.
	rec := func(mapType string, sub int, event, source string) {
		add(event)
		trace = append(trace, mapTraceEntry{EC: code, MapType: mapType, Code: sub, EventCode: event, Source: source})
	}
	// builtin records a hardcoded (non-table) resolution keyed on the raw EC.
	builtin := func(event string) {
		if event == "" {
			return
		}
		add(event)
		trace = append(trace, mapTraceEntry{EC: code, Code: code, EventCode: event, Source: traceBuiltin})
	}

	hasEndTime := func() bool {
		if alarm == nil {
			return false
		}
		s, _ := alarm["et"].(string)
		return s != ""
	}

	switch {
	case code == 1:
		builtin(mapHowenVideoLoss(detail))
	case code == 4 || code == 5:
		inputType, _ := numberOrNullInt(detailGet(detail, "num"))
		if v, ok := m.Input[inputType]; ok {
			rec("input", inputType, v, traceTable)
		} else if v := m.EventCode[code]; v != "" {
			// No matching input row — the alarm resolves via the event-code table
			// for this EC instead (e.g. a panic that arrives as ec=5 with no `num`
			// sub-field). Attribute it there so the trace reads "event_code 5 →
			// PANIC", not a misleading "input code 0 unmapped".
			rec("event_code", code, v, traceTable)
		} else {
			rec("input", inputType, "", traceFallback)
		}
	case code == 7 || code == 27 || code == 48:
		if hasEndTime() {
			builtin("SPEEDING:END")
		} else {
			builtin("SPEEDING:START")
		}
	case code == 11:
		builtin("PARKING:START")
	case code == 12:
		direction, _ := numberOrNullInt(detailGet(detail, "dt"))
		if v, ok := m.VibrationDirection[direction]; ok {
			rec("vibration_direction", direction, v, traceTable)
		} else {
			rec("vibration_direction", direction, "ALARM:VIBRATION", traceFallback)
		}
	case code == 13:
		// Geofence/electronic-fence crossing (spec §7), dispatched on the `st`
		// sub-field. NOTE: this was previously (incorrectly) keyed on ec=15.
		status, _ := numberOrNullInt(detailGet(detail, "st"))
		if v, ok := m.GeofenceStatus[status]; ok {
			rec("geofence_status", status, v, traceTable)
		} else {
			rec("geofence_status", status, "ZONE:UNKNOWN_EVENT", traceFallback)
		}
	case code == 15:
		// Abnormal door open/close (spec §8): st 0=close, 1=open. (ec=15 is the
		// door event in V4.0.0 — geofence moved to ec=13 above.)
		if status, ok := numberOrNullInt(detailGet(detail, "st")); ok && status == 0 {
			builtin("INPUT:DOOR_CLOSE")
		} else {
			builtin("ALARM:DOOR_OPEN")
		}
	case code == 22:
		// Swipe card (spec §13): tp 1=driver, 2=student, 3=invalid.
		switch sw, _ := numberOrNullInt(detailGet(detail, "tp")); sw {
		case 1:
			builtin("CARD:DRIVER")
		case 2:
			builtin("CARD:STUDENT")
		case 3:
			builtin("CARD:INVALID")
		default:
			builtin("CARD:SWIPE")
		}
	case code == 28:
		voltageType, _ := numberOrNullInt(detailGet(detail, "dt", "DT", "Dt", "type", "num"))
		if v, ok := m.Voltage[voltageType]; ok {
			rec("voltage", voltageType, v, traceTable)
		} else {
			rec("voltage", voltageType, "ALARM", traceFallback)
		}
	case code == 30:
		aiType, _ := numberOrNullInt(detailGet(detail, "tp"))
		if v, ok := m.DmsAdas[aiType]; ok {
			rec("dms_adas", aiType, v, traceTable)
		} else {
			rec("dms_adas", aiType, "ALARM", traceFallback)
		}
	case code == 32:
		if hasEndTime() {
			builtin("IDLING:END")
		} else {
			builtin("IDLING:START")
		}
	case code == 41:
		builtin("TRIP:START")
	case code == 43:
		builtin("TRIP:END")
	case code == 768:
		builtin("TRIP:END")
	case code == 1280 || code == 1281 || code == 1282:
		if sub, ok := parseHowenMediaAlarmSubtype(detail); ok {
			if v, ok := m.MediaAlarmSubtype[sub]; ok {
				rec("media_alarm_subtype", sub, v, traceTable)
			} else {
				rec("media_alarm_subtype", sub, "ALARM", traceFallback)
			}
		} else {
			rec("media_alarm_subtype", -1, "ALARM", traceFallback)
		}
	default:
		if v, ok := m.EventCode[code]; ok {
			rec("event_code", code, v, traceTable)
		} else {
			rec("event_code", code, "ALARM", traceFallback)
		}
	}

	if len(events) == 0 {
		events = []string{"ALARM"}
		if len(trace) == 0 {
			trace = []mapTraceEntry{{EC: code, Code: code, EventCode: "ALARM", Source: traceFallback}}
		}
	}
	return events, trace
}
