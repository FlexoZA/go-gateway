package cathexis

import (
	"context"
	"time"
)

// status.go builds the device-detail status snapshot and runs the background
// poller that keeps SD-card health and environment stats fresh. GPS/event
// telemetry is cached as it streams (recordStatus); SD health and environment are
// request/response queries (API §8, §10) the device only answers on demand, so a
// slow poller fetches them and caches the parsed result.

const (
	// firstStatusPollDelay staggers the first SD/environment poll a little after
	// connect so it doesn't race the welcome handshake.
	firstStatusPollDelay = 10 * time.Second
	// statusPollInterval is the steady-state refresh cadence (the environment
	// payload is ~1 KB; SD health is tiny — cheap enough on a slow interval).
	statusPollInterval = 10 * time.Minute
	statusPollTimeout  = 12 * time.Second
)

// statusPoller periodically fetches SD-card health and environment stats from the
// device and caches them for the status view. It runs only on the control
// connection, skips while the unit is in standby, and exits when the connection
// closes.
func (s *session) statusPoller() {
	defer func() { _ = recover() }()
	timer := time.NewTimer(firstStatusPollDelay)
	defer timer.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-timer.C:
			if s.lifecycle != "sleep" {
				s.pollStatus()
			}
			timer.Reset(statusPollInterval)
		}
	}
}

// pollStatus fetches SD health then environment, caching whatever succeeds.
func (s *session) pollStatus() {
	if sd := s.fetch("request_sd_health", "sd_health"); sd != nil {
		s.statusMu.Lock()
		s.sdHealth = sd
		s.sdHealthAt = time.Now().UTC()
		s.statusMu.Unlock()
	}
	if env := s.fetch("request_environment", "environment"); env != nil {
		s.statusMu.Lock()
		s.environment = env
		s.environmentAt = time.Now().UTC()
		s.statusMu.Unlock()
	}
}

// fetch issues one control request and returns the response payload, or nil on
// error/timeout (a transient poll failure is non-fatal).
func (s *session) fetch(cmdType, respType string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), statusPollTimeout)
	defer cancel()
	resp, err := s.request(ctx, cmdType, map[string]any{}, respType)
	if err != nil {
		s.conn.Deps.Log.With("tcp/cathexis").Debug(map[string]any{
			"event": "status_poll_failed", "serial": s.serial, "request": cmdType, "error": err.Error(),
		})
		return nil
	}
	return resp
}

// Status implements gateway.StatusReporter. The returned map IS the telemetry
// object the API surfaces (no extra nesting), with sections the admin renders:
// location, vehicle, sd_card, environment.
func (s *session) Status() (map[string]any, bool) {
	s.statusMu.Lock()
	latest, latestAt := s.latest, s.latestAt
	sd, sdAt := s.sdHealth, s.sdHealthAt
	env, envAt := s.environment, s.environmentAt
	s.statusMu.Unlock()

	snap := map[string]any{}
	if !latestAt.IsZero() {
		snap["updated_at"] = latestAt.Format(time.RFC3339)
	}
	if latest != nil {
		snap["location"] = locationSection(latest)
		snap["vehicle"] = map[string]any{
			"ignition": truthy(latest["ignition"]),
			"standby":  s.lifecycle == "sleep",
		}
	}
	if card := sdCardSection(sd); card != nil {
		card["polled_at"] = sdAt.Format(time.RFC3339)
		snap["sd_card"] = card
	}
	if e := environmentSection(env); e != nil {
		e["polled_at"] = envAt.Format(time.RFC3339)
		snap["environment"] = e
	}
	if len(snap) == 0 {
		return map[string]any{"updated_at": nil}, true
	}
	return snap, true
}

// locationSection shapes the cached GPS into the admin's location keys.
func locationSection(latest map[string]any) map[string]any {
	loc := map[string]any{}
	put := func(dst, src string) {
		if v, ok := latest[src]; ok {
			loc[dst] = v
		}
	}
	put("latitude", "latitude")
	put("longitude", "longitude")
	put("speed_kmh", "speed")
	put("satellites", "satellites")
	put("altitude_m", "altitude")
	put("bearing", "bearing")
	lat, okLat := toFloat(latest["latitude"])
	lon, okLon := toFloat(latest["longitude"])
	loc["positioned"] = okLat && okLon && (lat != 0 || lon != 0)
	return loc
}

// sdCardSection parses an sd_health response (API §8.2). Returns nil if absent.
func sdCardSection(sd map[string]any) map[string]any {
	if sd == nil {
		return nil
	}
	cardType := toString(sd["type"])
	out := map[string]any{
		"present": cardType != "" && cardType != "no_card",
		"type":    cardType,
	}
	if v := toString(sd["serial"]); v != "" {
		out["serial"] = v
	}
	if v, ok := toFloat(sd["use_pcnt"]); ok {
		out["use_percent"] = v
	}
	if v, ok := toFloat(sd["powercycle"]); ok {
		out["power_cycles"] = v
	}
	return out
}

// environmentSection parses an environment response (API §10.2) into a curated
// set of voltages, temperatures, loads, and signal levels. Returns nil if absent.
func environmentSection(env map[string]any) map[string]any {
	if env == nil {
		return nil
	}
	out := map[string]any{}
	num := func(dst, src string) {
		if v, ok := toFloat(env[src]); ok {
			out[dst] = v
		}
	}
	num("input_voltage_v", "inputV")
	num("input_current_a", "inputA")
	num("supercap_voltage_v", "superV")
	num("temp_device_c", "msp") // PCB controller temperature
	num("temp_case_c", "case-therm")
	num("temp_modem_c", "mdm-core")
	num("cpu_load_pct", "cpu_load")
	num("gpu_load_pct", "gpu_load")
	num("cell_level", "cell_level")
	num("wifi_level", "wifi_level")
	num("wifi_rssi", "wifi_rssi")
	if ssid := trimQuotes(toString(env["wifi_ssid"])); ssid != "" {
		out["wifi_ssid"] = ssid
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truthy coerces a JSON value (bool, number, "1"/"true") to a boolean.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || t == "true" || t == "True"
	default:
		return false
	}
}

// trimQuotes strips surrounding double quotes the firmware wraps some strings in
// (e.g. wifi_ssid is reported as "\"BT-R7A6R6\"").
func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
