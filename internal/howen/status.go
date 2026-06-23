package howen

import (
	"strconv"
	"time"
)

// status.go builds the device-detail snapshot served by GET /api/units/{serial}/
// status: a clean, sectioned view of the latest parsed device status (mobile/4G,
// module health, storage, GPS, vehicle IO, environment, motion sensor).

// Status returns the latest device-status snapshot. ok is true whenever the
// session exists (the device is connected); telemetry is empty until the device
// has sent its first status report.
func (s *session) Status() (map[string]any, bool) {
	s.statusMu.Lock()
	sd := s.latestStatus
	at := s.statusAt
	s.statusMu.Unlock()
	if sd == nil {
		return map[string]any{"updated_at": nil}, true
	}
	return buildStatusSnapshot(sd, at), true
}

func diskStatusLabel(v int) string {
	switch v {
	case 0:
		return "ok"
	case 1:
		return "error"
	case 2:
		return "full"
	case 3:
		return "not_present"
	case 4:
		return "formatting"
	default:
		return strconv.Itoa(v)
	}
}

func healthLabel(v int) string {
	if l, ok := howenModemHealthLabel[v]; ok {
		return l
	}
	return strconv.Itoa(v)
}

// buildStatusSnapshot turns the parsed status into a UI-friendly, sectioned map.
func buildStatusSnapshot(sd *howenStatusData, at time.Time) map[string]any {
	out := map[string]any{"updated_at": at.UTC().Format(time.RFC3339)}
	if sd.HasDeviceUTC {
		out["device_utc"] = sd.DeviceUTC
	}

	// Mobile network (4G/3G/…).
	if sd.MobileNetwork != nil || (sd.ModuleWorking != nil && sd.ModuleWorking.MobileNetworkHealth != nil) {
		net := map[string]any{}
		if mn := sd.MobileNetwork; mn != nil {
			radio, ok := howenRadioLabel[mn.NetworkType]
			if !ok {
				radio = strconv.Itoa(mn.NetworkType)
			}
			net["type"] = radio
			net["signal_level"] = mn.SignalIntensity // 0–10
			if mn.SignalIntensity >= 1 && mn.SignalIntensity <= 10 {
				net["signal_pct"] = mn.SignalIntensity * 10
			}
		}
		if sd.ModuleWorking != nil && sd.ModuleWorking.MobileNetworkHealth != nil {
			net["health"] = healthLabel(*sd.ModuleWorking.MobileNetworkHealth)
		}
		out["network"] = net
	}

	// Module health.
	if mw := sd.ModuleWorking; mw != nil {
		mods := map[string]any{}
		if mw.MobileNetworkHealth != nil {
			mods["mobile"] = healthLabel(*mw.MobileNetworkHealth)
		}
		if mw.LocationModuleHealth != nil {
			mods["gps"] = healthLabel(*mw.LocationModuleHealth)
		}
		if mw.WifiModuleHealth != nil {
			mods["wifi"] = healthLabel(*mw.WifiModuleHealth)
		}
		if mw.GsensorHealth != nil {
			mods["gsensor"] = healthLabel(*mw.GsensorHealth)
		}
		if mw.RecordingStatusRaw != nil {
			mods["recording_raw"] = *mw.RecordingStatusRaw
		}
		if len(mods) > 0 {
			out["modules"] = mods
		}
	}

	// GPS / location.
	if loc := sd.Location; loc != nil {
		l := map[string]any{
			"latitude":   loc.Latitude,
			"longitude":  loc.Longitude,
			"speed_kmh":  loc.Speed,
			"altitude_m": loc.Altitude,
			"satellites": loc.Satellites,
			"bearing":    loc.Bearing,
			"positioned": loc.Positioning != 0,
		}
		if loc.HasUTC {
			l["utc"] = loc.UTC
		}
		out["location"] = l
	}

	// Vehicle / IO.
	if bs := sd.BasicStatus; bs != nil {
		out["vehicle"] = map[string]any{
			"ignition":         bs.Ignition != 0,
			"brake":            bs.Brake != 0,
			"turn_left":        bs.TurnLeft != 0,
			"turn_right":       bs.TurnRight != 0,
			"reverse":          bs.Reverse != 0,
			"door_left_front":  bs.LeftFrontDoorOpen != 0,
			"door_right_front": bs.RightFrontDoorOpen != 0,
			"standby":          bs.SleepMode != 0,
		}
	}

	// Storage (SD/HDD).
	if hd := sd.HardDisk; hd != nil && len(hd.Disks) > 0 {
		disks := make([]map[string]any, 0, len(hd.Disks))
		for _, d := range hd.Disks {
			disks = append(disks, map[string]any{
				"id":      d.DiskID,
				"status":  diskStatusLabel(d.Status),
				"size_mb": d.SizeMB,
				"free_mb": d.RemainingMB,
			})
		}
		out["storage"] = disks
	}

	// Environment.
	if env := sd.Environment; env != nil {
		e := map[string]any{}
		putIntPtr(e, "temp_in_vehicle_c", env.InVehicleTempC)
		putIntPtr(e, "temp_out_vehicle_c", env.OutVehicleTempC)
		putIntPtr(e, "temp_motor_c", env.MotorTempC)
		putIntPtr(e, "temp_device_c", env.DeviceTempC)
		putIntPtr(e, "humidity_in_vehicle", env.InVehicleHum)
		putIntPtr(e, "humidity_out_vehicle", env.OutVehicleHum)
		if len(e) > 0 {
			out["environment"] = e
		}
	}

	// Motion sensor (G-sensor).
	if gs := sd.Gsensor; gs != nil {
		out["sensors"] = map[string]any{
			"x": gs.X, "y": gs.Y, "z": gs.Z, "tilt": gs.Tilt, "impact": gs.Impact,
		}
	}

	return out
}

func putIntPtr(m map[string]any, key string, v *int) {
	if v != nil {
		m[key] = *v
	}
}
