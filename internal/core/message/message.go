// Package message builds the ACM Universal JSON Message — the stable contract
// every unit type produces. It is a faithful port of the JS gateway's
// universalWebhookAdapter.js and is consumed by one or more sinks (PostgreSQL,
// webhook). Building is separated from delivery so a single built message (and a
// single per-device seq_no increment) feeds every sink.
//
// To reproduce the JS coercion rules exactly, payloads are handled as dynamic
// map[string]any values and the helpers below mirror valueOrNull / numberOrNull /
// boolOrNull / arrayOrEmpty one-to-one.
package message

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const gatewaySequenceMax = 1000

// Network is the inbound socket metadata attached by a protocol handler.
type Network struct {
	RemoteAddress string
	RemotePort    int
}

// Inbound is the common internal shape every protocol handler produces, matching
// the JS forwardBody: { serial, make, model, type, port, network, payload,
// receivedAt }.
type Inbound struct {
	Serial     string
	Make       string
	Model      string
	Type       string // "gps" | "event"
	Port       int
	Network    Network
	Payload    map[string]any
	ReceivedAt string // ISO8601; defaults to now when empty
}

// Builder produces universal messages and keeps the per-device sequence counter
// like the JS gateway. Safe for concurrent use.
type Builder struct {
	gateway     atomic.Pointer[string]
	TZOffsetHrs float64
	now         func() time.Time
	mu          sync.Mutex
	seqByDevice map[string]int
}

// NewBuilder constructs a message builder.
func NewBuilder(gateway string, tzOffsetHrs float64) *Builder {
	b := &Builder{
		TZOffsetHrs: tzOffsetHrs,
		now:         time.Now,
		seqByDevice: map[string]int{},
	}
	b.SetGateway(gateway)
	return b
}

// SetGateway atomically updates the gateway identifier embedded in every message
// (editable from the admin panel's server settings). Safe to call concurrently
// with Build.
func (b *Builder) SetGateway(gateway string) { b.gateway.Store(&gateway) }

// Gateway returns the current gateway identifier.
func (b *Builder) Gateway() string {
	if p := b.gateway.Load(); p != nil {
		return *p
	}
	return ""
}

// ---- output structs (field order mirrors the JS object literal exactly) ----

// Universal is the ACM Universal JSON Message.
type Universal struct {
	MessageVer   int            `json:"message_ver"`
	MessageType  string         `json:"message_type"`
	Gateway      any            `json:"gateway"`
	Port         any            `json:"port"`
	Transmission any            `json:"transmission"`
	Timestamp    any            `json:"timestamp"`
	Source       any            `json:"source"`
	SeqNo        int            `json:"seq_no"`
	Valid        bool           `json:"valid"`
	Device       universalDev   `json:"device"`
	Network      universalNet   `json:"network"`
	GSM          []universalGSM `json:"gsm"`
	Sims         []universalSim `json:"sims"`
	GPS          universalGPS   `json:"gps"`
	Events       []any          `json:"events"`
	Sensors      []any          `json:"sensors"`
	Inputs       any            `json:"inputs"`
	Outputs      any            `json:"outputs"`
	AuxInputs    []any          `json:"aux_inputs"`
	AnInputs     []any          `json:"an_inputs"`
	OBDII        universalOBD   `json:"obd_ii"`
	CanBus       []any          `json:"can_bus"`
}

type universalDev struct {
	Identifier any `json:"identifier"`
	IMEI       any `json:"imei"`
	SerialNo   any `json:"serial_no"`
	FirmVer    any `json:"firm_ver"`
	Type       any `json:"type"`
	Model      any `json:"model"`
}

type universalNet struct {
	RemoteIPv4 any `json:"remote_ipv4"`
	RemoteIPv6 any `json:"remote_ipv6"`
	RemotePort any `json:"remote_port"`
	Mac        any `json:"mac"`
}

type universalGSM struct {
	Cid       []any `json:"cid"`
	Lcid      []any `json:"lcid"`
	Lac       []any `json:"lac"`
	Carrier   any   `json:"carrier"`
	Mcc       []any `json:"mcc"`
	Mnc       []any `json:"mnc"`
	Rssi      []any `json:"rssi"`
	Rcpi      []any `json:"rcpi"`
	Ta        []any `json:"ta"`
	BsCount   []any `json:"bs_count"`
	SignalLvl any   `json:"signal_lvl"`
	SignalStr any   `json:"signal_str"`
	DataMode  any   `json:"data_mode"`
	Status    []any `json:"status"`
}

type universalSim struct {
	Msisdn any `json:"msisdn"`
	Iccid  any `json:"iccid"`
	Imsi   any `json:"imsi"`
}

type universalGPS struct {
	Timestamp  any    `json:"timestamp"`
	Latitude   any    `json:"latitude"`
	Longitude  any    `json:"longitude"`
	Altitude   any    `json:"altitude"`
	Speed      any    `json:"speed"`
	Heading    any    `json:"heading"`
	Satellites any    `json:"satellites"`
	Activity   string `json:"activity"`
	Odometer   any    `json:"odometer"`
	TripOdo    any    `json:"trip_odo"`
	Gnss       any    `json:"gnss"`
	Hdop       any    `json:"hdop"`
	Vdop       any    `json:"vdop"`
	Pdop       any    `json:"pdop"`
	Tdop       any    `json:"tdop"`
	Fix        []any  `json:"fix"`
}

type universalOBD struct {
	Mode01 []any `json:"mode_01"`
	Mode09 []any `json:"mode_09"`
}

// ---- accessors used by sinks for indexed storage columns ----

// SerialNo returns the device serial as a string ("" when absent).
func (u Universal) SerialNo() string { return toStr(u.Device.SerialNo) }

// IMEI returns the device IMEI as a string ("" when absent).
func (u Universal) IMEI() string { return toStr(u.Device.IMEI) }

// GPSTimestamp parses the gps.timestamp field, if present.
func (u Universal) GPSTimestamp() (time.Time, bool) {
	s, ok := u.GPS.Timestamp.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Latitude returns the gps latitude, if numeric.
func (u Universal) Latitude() (float64, bool) { f, ok := u.GPS.Latitude.(float64); return f, ok }

// Longitude returns the gps longitude, if numeric.
func (u Universal) Longitude() (float64, bool) { f, ok := u.GPS.Longitude.(float64); return f, ok }

// Build transforms an inbound message into the universal schema. Pure and
// deterministic except for the per-device sequence counter and the fallback
// "now" timestamp.
func (b *Builder) Build(in Inbound) Universal {
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	tz := b.TZOffsetHrs

	imei := valueOrNull(coalesce(payload["imei"], nil))
	serial := valueOrNull(coalesce(emptyToNil(in.Serial), payload["serial_no"]))
	latitude := numberOrNull(payload["latitude"])
	longitude := numberOrNull(payload["longitude"])
	positioning := boolOrNull(payload["positioning"])
	gpsTimestamp := coalesceAny(timestampFromUtc(payload["utc"], tz), valueOrNull(payload["timestamp"]))
	gatewayTimestamp := coalesceAny(
		timestampFromUtc(parseISOToUnixSeconds(in.ReceivedAt), tz),
		timestampFromUtc(float64(b.now().UnixNano())/1e9, tz),
	)
	hasPosition := latitude != nil && longitude != nil

	seqNo := b.nextSeq(imei, serial)

	validVal := hasPosition
	if positioning != nil {
		validVal = positioning.(bool)
	}

	// device.type / device.model with the jt808_19 special-case preserved.
	devType := strings.ToLower(toStr(firstTruthy(jt808Switch(in.Make, in.Model), coalesce(emptyToNil(in.Make), payload["type"]))))
	devModel := strings.ToLower(toStr(firstTruthy(jt808Switch(in.Make, in.Model), coalesce(emptyToNil(in.Model), payload["model"]))))

	identifier := any(nil)
	if isTruthy(imei) {
		identifier = "imei"
	} else if isTruthy(serial) {
		identifier = "serial_no"
	}

	return Universal{
		MessageVer:   1,
		MessageType:  "gps",
		Gateway:      valueOrNull(coalesce(nil, emptyToNil(b.Gateway()))),
		Port:         numberOrNull(in.Port),
		Transmission: valueOrNull(coalesce(nil, "tcp")),
		Timestamp:    gatewayTimestamp,
		Source:       valueOrNull(coalesce(nil, "device")),
		SeqNo:        seqNo,
		Valid:        validVal,
		Device: universalDev{
			Identifier: identifier,
			IMEI:       imei,
			SerialNo:   serial,
			FirmVer:    valueOrNull(coalesce(payload["firm_ver"], payload["firmware_version"])),
			Type:       valueOrNull(devType),
			Model:      valueOrNull(devModel),
		},
		Network: resolveNetwork(in),
		GSM: []universalGSM{{
			Cid: []any{}, Lcid: []any{}, Lac: arrayOrEmpty(payload["lac"]),
			Carrier: valueOrNull(payload["carrier"]),
			Mcc:     []any{}, Mnc: []any{}, Rssi: []any{}, Rcpi: []any{}, Ta: []any{}, BsCount: []any{},
			SignalLvl: numberOrNull(coalesce(payload["signal_lvl"], payload["signal"])),
			SignalStr: numberOrNull(payload["signal_str"]),
			DataMode:  valueOrNull(payload["data_mode"]),
			Status:    resolveGsmStatusStrings(payload),
		}},
		Sims: []universalSim{{
			Msisdn: valueOrNull(payload["msisdn"]),
			Iccid:  valueOrNull(payload["iccid"]),
			Imsi:   valueOrNull(payload["imsi"]),
		}},
		GPS: universalGPS{
			Timestamp:  gpsTimestamp,
			Latitude:   latitude,
			Longitude:  longitude,
			Altitude:   numberOrNull(payload["altitude"]),
			Speed:      numberOrNull(payload["speed"]),
			Heading:    numberOrNull(coalesce(payload["heading"], payload["bearing"])),
			Satellites: numberOrNull(payload["satellites"]),
			Activity:   resolveActivity(payload),
			Odometer:   numberOrNull(coalesce(payload["odometer"], payload["mileage_km"])),
			TripOdo:    numberOrNull(payload["trip_odo"]),
			Gnss:       boolOrNull(coalesce(payload["gnss"], payload["positioning"])),
			Hdop:       numberOrNull(coalesce(payload["hdop"], payload["accuracy"])),
			Vdop:       numberOrNull(payload["vdop"]),
			Pdop:       numberOrNull(payload["pdop"]),
			Tdop:       numberOrNull(payload["tdop"]),
			Fix:        resolveFix(payload),
		},
		Events:    resolveEvents(payload),
		Sensors:   resolveSensors(payload),
		Inputs:    valueOrNull(payload["inputs"]),
		Outputs:   valueOrNull(payload["outputs"]),
		AuxInputs: arrayOrEmpty(payload["aux_inputs"]),
		AnInputs:  arrayOrEmpty(payload["an_inputs"]),
		OBDII:     universalOBD{Mode01: arrayOrEmpty(nestedField(payload, "obd_ii", "mode_01")), Mode09: arrayOrEmpty(nestedField(payload, "obd_ii", "mode_09"))},
		CanBus:    arrayOrEmpty(payload["can_bus"]),
	}
}

func (b *Builder) nextSeq(imei, serial any) int {
	key := "serial:unknown"
	if isTruthy(imei) {
		key = "imei:" + toStr(imei)
	} else if isTruthy(serial) {
		key = "serial:" + toStr(serial)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	last := b.seqByDevice[key]
	next := 1
	if last < gatewaySequenceMax {
		next = last + 1
	}
	b.seqByDevice[key] = next
	return next
}

func jt808Switch(mk, model string) any {
	if mk == "jt808_19" {
		return model
	}
	return nil
}

func resolveActivity(payload map[string]any) string {
	if s, ok := payload["activity"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	ign := boolOrNull(payload["ignition"])
	spd := numberOrNull(payload["speed"])
	if ign != nil && ign.(bool) && spd != nil && spd.(float64) > 0 {
		return "driving"
	}
	if ign != nil && ign.(bool) {
		return "idling"
	}
	if ign != nil && !ign.(bool) {
		return "parked"
	}
	return "unknown"
}

func resolveFix(payload map[string]any) []any {
	out := []any{}
	for _, item := range arrayOrEmpty(payload["fix"]) {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, item)
		}
	}
	if len(out) > 0 {
		return out
	}
	positioning := boolOrNull(payload["positioning"])
	if positioning != nil && positioning.(bool) {
		return []any{"fixed"}
	}
	if positioning != nil && !positioning.(bool) {
		return []any{"invalid_fix"}
	}
	return []any{}
}

func resolveEvents(payload map[string]any) []any {
	events := []any{}
	rawEvent := payload["event"]
	if list, ok := rawEvent.([]any); ok {
		tripDetail, _ := payload["howen_event_detail"].(map[string]any)
		howenEventCode := numberOrNull(payload["howen_event_code"])
		isHowenTrip := false
		isHowenGsensor := false
		if howenEventCode != nil {
			c := howenEventCode.(float64)
			isHowenTrip = c == 41 || c == 43 || c == 768
			isHowenGsensor = c == 12
		}
		for _, ev := range list {
			name, ok := ev.(string)
			if !ok || strings.TrimSpace(name) == "" {
				continue
			}
			name = strings.TrimSpace(name)
			if isHowenTrip && strings.HasPrefix(name, "TRIP:") && tripDetail != nil {
				rows := [][]any{}
				pushNum := func(k, src string) {
					if v := numberOrNull(tripDetail[src]); v != nil {
						rows = append(rows, []any{k, v})
					}
				}
				pushNum("duration_seconds", "dur")
				pushNum("mileage_km", "mile")
				pushNum("avg_speed_kmh", "avg")
				pushNum("max_speed_kmh", "max")
				pushNum("start_latitude", "slat")
				pushNum("start_longitude", "slng")
				if v := valueOrNull(tripDetail["drid"]); v != nil {
					rows = append(rows, []any{"driver_id", v})
				}
				if len(rows) > 0 {
					events = append(events, []any{name, toAnySlice(rows)})
					continue
				}
			}
			if isHowenGsensor {
				rows := [][]any{}
				for _, ax := range []struct{ k, src string }{{"X", "acc_x"}, {"Y", "acc_y"}, {"Z", "acc_z"}} {
					if v := numberOrNull(payload[ax.src]); v != nil {
						rows = append(rows, []any{ax.k, v})
					}
				}
				if len(rows) > 0 {
					events = append(events, []any{name, toAnySlice(rows)})
					continue
				}
			}
			events = append(events, []any{name})
		}
	} else if s, ok := rawEvent.(string); ok && strings.TrimSpace(s) != "" {
		events = append(events, []any{strings.TrimSpace(s)})
	}

	hasMappedVideoSignalLoss := false
	for _, e := range events {
		if pair, ok := e.([]any); ok && len(pair) > 0 {
			if name, ok := pair[0].(string); ok && strings.HasPrefix(name, "VIDEO:SIGNAL_LOSS:CHANNEL:") {
				hasMappedVideoSignalLoss = true
			}
		}
	}

	rawAlarmEvents := []struct {
		name string
		val  any
	}{
		{"VIDEO_SIGNAL_LOSS_ALARM", payload["video_signal_loss_alarm"]},
		{"MEMORY_FAULT_ALARM", payload["memory_fault_alarm"]},
	}
	for _, ra := range rawAlarmEvents {
		if ra.name == "VIDEO_SIGNAL_LOSS_ALARM" && hasMappedVideoSignalLoss {
			continue
		}
		v := numberOrNull(ra.val)
		if v == nil || v.(float64) == 0 {
			continue
		}
		events = append(events, []any{ra.name, v})
	}

	return events
}

func resolveSensors(payload map[string]any) []any {
	sensors := []any{}
	ign := boolOrNull(payload["ignition"])
	var ignVal any
	if ign != nil {
		if ign.(bool) {
			ignVal = "on"
		} else {
			ignVal = "off"
		}
	}
	fields := []struct {
		name string
		val  any
	}{
		{"ignition", ignVal},
		{"accel_x", payload["acc_x"]},
		{"accel_y", payload["acc_y"]},
		{"accel_z", payload["acc_z"]},
		{"disk_status", payload["disk_status"]},
		{"disk_size_mb", payload["disk_size_mb"]},
		{"disk_free_mb", payload["disk_free_mb"]},
		{"alarm_video_loss_mask", payload["alarm_video_loss_mask"]},
		{"alarm_motion_detection_mask", payload["alarm_motion_detection_mask"]},
		{"alarm_video_blind_mask", payload["alarm_video_blind_mask"]},
		{"alarm_input_trigger_mask", payload["alarm_input_trigger_mask"]},
		{"temp_in_vehicle_c", payload["temp_in_vehicle_c"]},
		{"temp_out_vehicle_c", payload["temp_out_vehicle_c"]},
		{"temp_motor_c", payload["temp_motor_c"]},
		{"temp_device_c", payload["temp_device_c"]},
		{"humidity_in_vehicle", payload["humidity_in_vehicle"]},
		{"humidity_out_vehicle", payload["humidity_out_vehicle"]},
		{"fuel_level", payload["fuel_l"]},
		{"aux_fuel_l", payload["aux_fuel_l"]},
		// OBD/CAN telemetry (e.g. Howen datahub, ec=771). Optional; absent on
		// most messages — receivers must tolerate missing sensors.
		{"engine_rpm", payload["engine_rpm"]},
		{"obd_speed", payload["obd_speed"]},
		{"coolant_temp_c", payload["coolant_temp_c"]},
		{"accel_pedal_pct", payload["accel_pedal_pct"]},
		{"obd_distance", payload["obd_distance"]},
		{"trip_fuel_used_cc", payload["trip_fuel_used_cc"]},
		{"speed_recorder", payload["speed_recorder"]},
		{"io_status_bits", payload["io_status_bits"]},
		{"extended_vehicle_signal_status", payload["extended_vehicle_signal_status"]},
	}
	for _, f := range fields {
		if f.val == nil {
			continue
		}
		if s, ok := f.val.(string); ok && s == "" {
			continue
		}
		sensors = append(sensors, []any{f.name, f.val})
	}
	return sensors
}

func resolveNetwork(in Inbound) universalNet {
	remoteAddress := in.Network.RemoteAddress
	isIPv6 := strings.Contains(remoteAddress, ":")
	var v4, v6 any
	if isIPv6 {
		v6 = remoteAddress
		v4 = nil
	} else {
		v4 = valueOrNull(remoteAddress)
		v6 = nil
	}
	return universalNet{
		RemoteIPv4: v4,
		RemoteIPv6: v6,
		RemotePort: numberOrNull(in.Network.RemotePort),
		Mac:        nil,
	}
}

func resolveGsmStatusStrings(payload map[string]any) []any {
	out := []any{}
	for _, item := range arrayOrEmpty(payload["gsm_status"]) {
		switch v := item.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, strings.TrimSpace(v))
			}
		case []any:
			if len(v) >= 1 {
				key := strings.TrimSpace(toStr(v[0]))
				var val any
				if len(v) >= 2 {
					val = v[1]
				}
				if val != nil && len(toStr(val)) > 0 {
					out = append(out, fmt.Sprintf("%s:%s", key, toStr(val)))
				} else if key != "" {
					out = append(out, key)
				}
			}
		}
	}
	return out
}

// ---- coercion helpers (mirror the JS adapter) ----

func valueOrNull(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	return v
}

func numberOrNull(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return nil
		}
		return t
	case float32:
		return numberOrNull(float64(t))
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case bool:
		if t {
			return float64(1)
		}
		return float64(0)
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return nil
		}
		normalized := trimmed
		if strings.Contains(trimmed, ",") && !strings.Contains(trimmed, ".") {
			normalized = strings.Replace(trimmed, ",", ".", 1)
		}
		parsed, err := strconv.ParseFloat(normalized, 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil
		}
		return parsed
	default:
		return nil
	}
}

func boolOrNull(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool:
		return t
	case float64:
		return t != 0
	case float32:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case string:
		n := strings.ToLower(strings.TrimSpace(t))
		switch n {
		case "":
			return nil
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
		return nil
	default:
		return nil
	}
}

func arrayOrEmpty(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	default:
		return []any{}
	}
}

func timestampFromUtc(value any, tzOffsetHours float64) any {
	secsAny := numberOrNull(value)
	if secsAny == nil {
		return nil
	}
	seconds := secsAny.(float64)
	if seconds <= 0 {
		return nil
	}
	offsetMs := int64(tzOffsetHours * 60 * 60 * 1000)
	totalMs := int64(seconds*1000) + offsetMs
	t := time.UnixMilli(totalMs).UTC()
	stamp := t.Format("2006-01-02T15:04:05")
	sign := "+"
	if tzOffsetHours < 0 {
		sign = "-"
	}
	abs := math.Abs(tzOffsetHours)
	hours := int(math.Floor(abs))
	minutes := int(math.Round((abs - math.Floor(abs)) * 60))
	return fmt.Sprintf("%s%s%02d:%02d", stamp, sign, hours, minutes)
}

func parseISOToUnixSeconds(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixNano()) / 1e9
		}
	}
	return nil
}

// ---- small generic helpers ----

func coalesce(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

func coalesceAny(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

func firstTruthy(a, b any) any {
	if isTruthy(a) {
		return a
	}
	return b
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return true
	}
}

func toStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func toAnySlice(rows [][]any) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

func nestedField(m map[string]any, key, sub string) any {
	if inner, ok := m[key].(map[string]any); ok {
		return inner[sub]
	}
	return nil
}
