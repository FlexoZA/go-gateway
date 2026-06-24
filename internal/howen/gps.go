package howen

import "strconv"

// H-Protocol §3.2 network type labels.
var howenRadioLabel = map[int]string{
	0: "unknown", 1: "wired", 2: "wifi", 3: "2g", 4: "3g", 5: "4g", 6: "5g",
	7: "wifi_cell_proxy", 8: "cable_cell_proxy",
}

var howenModemHealthLabel = map[int]string{
	0: "unknown", 1: "normal", 2: "abnormal", 3: "not_exist",
}

// howenMobileNetworkToPayloadFields maps the parsed mobile/modem blocks into the
// universal payload fields (signal_lvl, signal_str, data_mode, gsm_status).
// Ported from howenCodec.js howenMobileNetworkToPayloadFields.
func howenMobileNetworkToPayloadFields(mn *howenMobileNetwork, mw *howenModuleWorking) map[string]any {
	status := []any{}

	if mw != nil && mw.MobileNetworkHealth != nil {
		h := *mw.MobileNetworkHealth
		label, ok := howenModemHealthLabel[h]
		if !ok {
			label = strconv.Itoa(h)
		}
		status = append(status, "howen_modem_health:"+label)
		if h == 1 {
			status = append(status, "network")
		}
	}

	if mn == nil {
		if len(status) > 0 {
			return map[string]any{"gsm_status": status}
		}
		return map[string]any{}
	}

	out := map[string]any{}
	signal := mn.SignalIntensity
	out["signal_lvl"] = float64(signal)
	if signal >= 1 && signal <= 10 {
		out["signal_str"] = float64(signal * 10)
	}
	if signal == 0 {
		status = append(status, "howen_signal:invalid")
	}

	nt := mn.NetworkType
	out["data_mode"] = strconv.Itoa(nt)
	radio, ok := howenRadioLabel[nt]
	if !ok {
		radio = strconv.Itoa(nt)
	}
	status = append(status, "howen_network_type:"+strconv.Itoa(nt), "howen_radio:"+radio)
	if nt >= 3 && nt <= 8 {
		status = append(status, "data")
		if !containsAny(status, "network") {
			status = append(status, "network")
		}
	}

	if len(status) > 0 {
		out["gsm_status"] = status
	}
	return out
}

func containsAny(s []any, want string) bool {
	for _, v := range s {
		if str, ok := v.(string); ok && str == want {
			return true
		}
	}
	return false
}

// statusCommonFields builds the shared GPS/telemetry payload fields from a parsed
// status block, mirroring the field set assembled in handleGpsStatus /
// handleAlarmData. Keys are only set when the source value is present (matching
// the JS undefined-skips-in-adapter behaviour).
func statusCommonFields(status *howenStatusData, imei string) map[string]any {
	p := map[string]any{}
	if status == nil {
		setIMEI(p, imei)
		return p
	}

	if loc := status.Location; loc != nil {
		p["latitude"] = loc.Latitude
		p["longitude"] = loc.Longitude
		p["speed"] = loc.Speed
		p["altitude"] = loc.Altitude
		p["accuracy"] = loc.Accuracy
		p["bearing"] = float64(loc.Bearing)
		p["satellites"] = float64(loc.Satellites)
		if loc.HasUTC {
			p["utc"] = float64(loc.UTC)
		} else {
			p["utc"] = nil
		}
		p["positioning"] = float64(loc.Positioning)
		p["howen_location_type"] = float64(loc.LocationType)
	}

	for k, v := range howenMobileNetworkToPayloadFields(status.MobileNetwork, status.ModuleWorking) {
		p[k] = v
	}

	if gs := status.Gsensor; gs != nil {
		p["acc_x"] = gs.X
		p["acc_y"] = gs.Y
		p["acc_z"] = gs.Z
	}

	if hd := status.HardDisk; hd != nil && len(hd.Disks) > 0 {
		d := hd.Disks[0]
		p["disk_status"] = float64(d.Status)
		p["disk_size_mb"] = float64(d.SizeMB)
		p["disk_free_mb"] = float64(d.RemainingMB)
	}

	if as := status.AlarmStatus; as != nil {
		setIntPtr(p, "alarm_video_loss_mask", as.VideoLossMask)
		setIntPtr(p, "alarm_motion_detection_mask", as.MotionDetectMask)
		setIntPtr(p, "alarm_video_blind_mask", as.VideoBlindMask)
		setIntPtr(p, "alarm_input_trigger_mask", as.InputTriggerMask)
	}

	if env := status.Environment; env != nil {
		setIntPtr(p, "temp_in_vehicle_c", env.InVehicleTempC)
		setIntPtr(p, "temp_out_vehicle_c", env.OutVehicleTempC)
		setIntPtr(p, "temp_motor_c", env.MotorTempC)
		setIntPtr(p, "temp_device_c", env.DeviceTempC)
		setIntPtr(p, "humidity_in_vehicle", env.InVehicleHum)
		setIntPtr(p, "humidity_out_vehicle", env.OutVehicleHum)
	}

	setIMEI(p, imei)
	if bs := status.BasicStatus; bs != nil {
		p["ignition"] = float64(bs.Ignition)
	}
	return p
}

// buildGpsPayload assembles the payload for a GPS_STATUS message.
func buildGpsPayload(status *howenStatusData, imei string) map[string]any {
	p := statusCommonFields(status, imei)
	p["name"] = "gps"
	return p
}

// buildEventPayload assembles the payload for an ALARM_DATA message, resolving the
// event codes against the device model's mapping table (falling back to the unit
// default then the built-in defaults).
func buildEventPayload(model string, ap *alarmPayload, imei string) map[string]any {
	p := statusCommonFields(ap.Status, imei)
	p["name"] = "event"

	codes := mapHowenEventCodes(model, ap.EC, ap.Detail, ap.Alarm)
	events := make([]any, len(codes))
	for i, e := range codes {
		events[i] = e
	}
	p["event"] = events

	// utc = status.location?.utc || eventStartUtc || deviceUtc || 0
	utc := int64(0)
	switch {
	case ap.Status != nil && ap.Status.Location != nil && ap.Status.Location.HasUTC && ap.Status.Location.UTC != 0:
		utc = ap.Status.Location.UTC
	case ap.EventStartUTC != nil && *ap.EventStartUTC != 0:
		utc = *ap.EventStartUTC
	case ap.DeviceUTC != nil && *ap.DeviceUTC != 0:
		utc = *ap.DeviceUTC
	}
	p["utc"] = float64(utc)

	if ap.EC != nil {
		p["howen_event_code"] = ap.EC
	}
	if ap.DetailRaw != nil {
		p["howen_event_detail"] = ap.DetailRaw
	}
	if ap.AlarmRaw != nil {
		p["howen_alarm"] = ap.AlarmRaw
	}
	if ap.EventStartUTC != nil {
		p["event_start_utc"] = float64(*ap.EventStartUTC)
	}
	if ap.EventEndUTC != nil {
		p["event_end_utc"] = float64(*ap.EventEndUTC)
	}
	return p
}

func setIntPtr(p map[string]any, key string, v *int) {
	if v != nil {
		p[key] = float64(*v)
	}
}

func setIMEI(p map[string]any, imei string) {
	if imei != "" {
		p["imei"] = imei
	} else {
		p["imei"] = nil
	}
}
