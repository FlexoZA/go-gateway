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

var defaultInputEventMap = map[int]string{
	1: "PANIC", 2: "ALARM:DOOR_OPEN", 3: "ALARM:DOOR_OPEN", 4: "ALARM:DOOR_OPEN",
	9: "ALARM", 10: "ALARM", 11: "ALARM:FOOTBRAKE", 12: "ALARM",
	14: "ALARM:DOOR_OPEN", 15: "ALARM:DOOR_OPEN", 16: "ALARM:DOOR_OPEN", 22: "ALARM",
}

var defaultVibrationDirectionEventMap = map[int]string{
	1: "ALARM:VIBRATION", 2: "ALARM:VIBRATION", 3: "ALARM:VIBRATION",
	4: "COLLISION", 5: "COLLISION:TURN_OVER", 6: "HARSH:CORNERING",
	7: "HARSH:ACCELERATION", 8: "HARSH:BRAKING",
}

var defaultGeofenceStatusEventMap = map[int]string{
	0: "ZONE:ENTER", 1: "ZONE:EXIT", 2: "ZONE:SPEEDING", 3: "SPEEDING",
	4: "ZONE:UNKNOWN_EVENT", 5: "ZONE:UNKNOWN_EVENT", 6: "ZONE:FORBIDDEN_STOP",
	7: "ZONE:FORBIDDEN_STOP", 8: "ZONE:DELAYED_STOP", 9: "ZONE:ENTER", 10: "ZONE:EXIT",
}

var defaultVoltageEventMap = map[int]string{
	1: "BATTERY:LOW:EXT", 2: "BATTERY:ABNORMAL:EXT", 3: "BATTERY:DISCONNECTED",
	4: "BATTERY:CONNECTED", 5: "BATTERY:DISCONNECTED", 6: "BATTERY:LOW:EXT",
	7: "BATTERY:ABNORMAL:EXT",
}

var defaultDmsAdasEventMap = map[int]string{
	1: "COLLISION", 2: "AI:LANE_DEPARTURE", 3: "FOLLOWING:DISTANCE:VIOLATION",
	4: "COLLISION", 5: "HARSH:DRIVING", 6: "AI:TRAFFIC_SIGN:VIOLATION",
	7: "HARSH:ACCELERATION", 8: "HARSH:BRAKING", 9: "FOLLOWING:DISTANCE:VIOLATION",
	16: "COLLISION", 17: "COLLISION", 18: "FOLLOWING:DISTANCE:VIOLATION",
	19: "AI:LANE_DEPARTURE", 20: "AI:LANE_DEPARTURE", 21: "COLLISION",
	33: "AI:FATIGUE", 34: "AI:CELLPHONE", 35: "AI:SMOKING", 36: "AI:DISTRACTION",
	37: "AI:DRIVER:ABNORMAL", 39: "AI:EYES_CLOSED:CRITICAL", 40: "AI:YAWN:CRITICAL",
	49: "AI:DRIVER:CHANGE", 65: "AI:EYES_CLOSED", 66: "AI:YAWN", 67: "AI:LENS_COVERED",
	68: "AI:DISTRACTION", 69: "AI:SEATBELT", 70: "AI:NO_DRIVER", 72: "AI:DRIVER:CHANGE",
	73: "AI:TAMPER", 80: "AI:EYES_DETECTION_FAILED", 82: "AI:NO_DRIVER", 83: "AI:NO_DRIVER",
	85: "AI:DRIVER:MASK", 96: "AI:BLINDSPOT", 97: "AI:BLINDSPOT", 98: "AI:BLINDSPOT",
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

var defaultEventCodeMap = map[int]string{
	1: "VIDEO_LOSS", 2: "ALARM", 3: "ALARM:TAMPERING", 4: "ALARM", 5: "PANIC",
	6: "ALARM", 7: "SPEEDING", 8: "TEMP:LOW", 9: "TEMP:HIGH", 11: "PARKING:START",
	12: "ALARM:VIBRATION", 15: "ALARM", 16: "ALARM:HARDWARE:FAULT", 19: "IGNITION:OFF",
	22: "MESSAGE:BUFFERED", 24: "HARSH:ACCELERATION", 25: "HARSH:BRAKING", 26: "SPEED:LOW",
	27: "SPEEDING", 28: "ALARM", 30: "ALARM", 31: "IGNITION:ON", 32: "IDLING:START",
	41: "TRIP:START", 43: "TRIP:END", 48: "SPEEDING", 49: "ALARM", 59: "DEVICE:WAKEUP",
	60: "ALARM:HARDWARE:FAULT", 61: "ALARM", 62: "AI:FATIGUE", 78: "AI:TAMPER",
	80: "ALARM:HARDWARE:FAULT", 768: "TRIP:UNKNOWN", 769: "ALARM", 770: "ALARM:HARDWARE:FAULT",
	771: "ALARM", 772: "ALARM:HARDWARE:FAULT", 773: "WATER:FLOW",
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

// DefaultMappingEntries flattens the built-in defaults for seeding the database.
// Entries are emitted in a stable (map_type, code) order.
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

// mapHowenEventCodes ports howenCodec.js mapHowenEventCodes. `alarm` is the raw
// alarm JSON object (used for the `et` end-time check). It reads the active mapping
// set for the device's model (which may have been overridden from the database),
// falling back to the unit default then the built-in defaults.
func mapHowenEventCodes(model string, eventCode any, detail map[string]any, alarm map[string]any) []string {
	code, ok := numberOrNullInt(eventCode)
	if !ok {
		return []string{"ALARM"}
	}
	m := mappingsForModel(model)
	events := []string{}
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

	hasEndTime := func() bool {
		if alarm == nil {
			return false
		}
		s, _ := alarm["et"].(string)
		return s != ""
	}

	switch {
	case code == 1:
		add(mapHowenVideoLoss(detail))
	case code == 4 || code == 5:
		inputType, _ := numberOrNullInt(detailGet(detail, "num"))
		if v, ok := m.Input[inputType]; ok {
			add(v)
		} else {
			add(m.EventCode[code])
		}
	case code == 7 || code == 27 || code == 48:
		if hasEndTime() {
			add("SPEEDING:END")
		} else {
			add("SPEEDING:START")
		}
	case code == 11:
		add("PARKING:START")
	case code == 12:
		direction, _ := numberOrNullInt(detailGet(detail, "dt"))
		if v, ok := m.VibrationDirection[direction]; ok {
			add(v)
		} else {
			add("ALARM:VIBRATION")
		}
	case code == 15:
		status, _ := numberOrNullInt(detailGet(detail, "st"))
		if v, ok := m.GeofenceStatus[status]; ok {
			add(v)
		} else {
			add("ZONE:UNKNOWN_EVENT")
		}
	case code == 28:
		voltageType, _ := numberOrNullInt(detailGet(detail, "dt", "DT", "Dt", "type", "num"))
		if v, ok := m.Voltage[voltageType]; ok {
			add(v)
		} else {
			add("ALARM")
		}
	case code == 30:
		aiType, _ := numberOrNullInt(detailGet(detail, "tp"))
		if v, ok := m.DmsAdas[aiType]; ok {
			add(v)
		} else {
			add("ALARM")
		}
	case code == 32:
		if hasEndTime() {
			add("IDLING:END")
		} else {
			add("IDLING:START")
		}
	case code == 41:
		add("TRIP:START")
	case code == 43:
		add("TRIP:END")
	case code == 768:
		add("TRIP:END")
	case code == 1280 || code == 1281 || code == 1282:
		if sub, ok := parseHowenMediaAlarmSubtype(detail); ok {
			if v, ok := m.MediaAlarmSubtype[sub]; ok {
				add(v)
			} else {
				add("ALARM")
			}
		} else {
			add("ALARM")
		}
	default:
		if v, ok := m.EventCode[code]; ok {
			add(v)
		} else {
			add("ALARM")
		}
	}

	if len(events) == 0 {
		return []string{"ALARM"}
	}
	return events
}
