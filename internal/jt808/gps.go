package jt808

import "encoding/binary"

// buildLocationPayload normalizes a decoded 0x0200 location into the field map
// the universal message builder understands. Returns the payload and whether any
// event code was resolved (so the caller can emit "event" vs "gps").
func (proto *Protocol) buildLocationPayload(loc location, model string) (map[string]any, bool) {
	p := map[string]any{
		"latitude":    loc.Latitude,
		"longitude":   loc.Longitude,
		"speed":       loc.Speed,
		"bearing":     float64(loc.Direction),
		"altitude":    float64(loc.Altitude),
		"utc":         float64(loc.TimeUTC),
		"positioning": loc.Status&(1<<1) != 0, // status bit 1: GPS fix valid
		"ignition":    loc.Status&(1<<0) != 0, // status bit 0: ACC on
	}

	// Additional-info TLV telemetry.
	if v, ok := loc.TLVs[0x01]; ok && len(v) >= 4 { // mileage, 1/10 km
		p["mileage_km"] = float64(binary.BigEndian.Uint32(v[0:4])) / 10.0
	}
	if v, ok := loc.TLVs[0x02]; ok && len(v) >= 2 { // fuel, 1/10 L
		p["fuel_l"] = float64(binary.BigEndian.Uint16(v[0:2])) / 10.0
	}
	if v, ok := loc.TLVs[0xec]; ok && len(v) >= 2 { // auxiliary fuel, 1/10 L
		p["aux_fuel_l"] = float64(binary.BigEndian.Uint16(v[0:2])) / 10.0
	}
	if v, ok := loc.TLVs[0x03]; ok && len(v) >= 2 { // recorder speed, 1/10 km/h
		p["speed_recorder"] = float64(binary.BigEndian.Uint16(v[0:2])) / 10.0
	}
	if v, ok := loc.TLVs[0x30]; ok && len(v) >= 1 { // GSM signal strength
		p["signal"] = float64(v[0])
	}
	if v, ok := loc.TLVs[0x31]; ok && len(v) >= 1 { // satellite count
		p["satellites"] = float64(v[0])
	}

	events := proto.resolveEvents(loc, model)
	if len(events) > 0 {
		ev := make([]any, len(events))
		for i, e := range events {
			ev[i] = e
		}
		p["event"] = ev
		return p, true
	}
	return p, false
}
