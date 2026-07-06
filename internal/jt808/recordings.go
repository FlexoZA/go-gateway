package jt808

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// recordings.go implements the recorded-footage file query: platform sends a
// resource-list query (0x9205) and the device replies with the list (0x1205).

// maxQueryDays caps how many per-day sub-queries a single QueryRecordings will
// issue, bounding the round-trips (and time) for an over-wide window.
const maxQueryDays = 31

// QueryRecordings asks the device what footage it has for a camera/time window.
// camera < 0 queries all channels. profile is advisory; we query all stream types
// and report each entry's own stream type.
//
// The N62 firmware only answers a 0x9205 query for a window within a single
// device-local calendar day: any window that crosses local midnight comes back
// empty. So we split [startUTC,endUTC] into per-local-day chunks, query each, and
// concatenate — a range like "last 7 days" becomes up to 7 same-day queries.
func (s *session) QueryRecordings(ctx context.Context, camera, profile int, startUTC, endUTC int64) ([]gateway.Recording, error) {
	if !s.approved {
		return nil, gateway.ErrNotConnected
	}
	if endUTC <= startUTC {
		return []gateway.Recording{}, nil
	}
	tz := s.tzOffset()
	out := []gateway.Recording{}
	for i, win := range splitLocalDays(startUTC, endUTC, tz, maxQueryDays) {
		recs, err := s.queryRecordingsDay(ctx, camera, win[0], win[1], tz)
		if err != nil {
			// Return whatever we already gathered on the first failure rather than
			// discarding earlier days; surface the error only if nothing landed.
			if len(out) > 0 {
				s.log().Debug(map[string]any{"event": "query_recordings_partial", "serial": s.serial, "day": i, "error": err.Error()})
				return out, nil
			}
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// splitLocalDays breaks [startUTC,endUTC] into sub-windows that each fall inside
// one device-local calendar day (offsetHours from UTC), so no sub-window crosses
// local midnight. At most maxDays windows are returned (the tail is dropped).
func splitLocalDays(startUTC, endUTC int64, offsetHours float64, maxDays int) [][2]int64 {
	off := int64(offsetHours * 3600)
	const day = 86400
	// Local-midnight boundary at or before the local start instant, expressed back
	// in UTC epoch seconds.
	localStart := startUTC + off
	dayStartLocal := localStart - ((localStart%day)+day)%day // floor to local 00:00
	wins := [][2]int64{}
	for b := dayStartLocal; b <= endUTC+off && len(wins) < maxDays; b += day {
		ws := b - off       // this local day's 00:00 in UTC
		we := b + day - off // next local day's 00:00 in UTC
		if ws < startUTC {
			ws = startUTC
		}
		if we > endUTC {
			we = endUTC
		}
		if we > ws {
			wins = append(wins, [2]int64{ws, we})
		}
	}
	return wins
}

// queryRecordingsDay issues a single 0x9205 for a window that must lie within one
// device-local day, and returns the parsed 0x1205 entries.
func (s *session) queryRecordingsDay(ctx context.Context, camera int, startUTC, endUTC int64, tz float64) ([]gateway.Recording, error) {
	channel := 0
	if camera >= 0 {
		channel = camera + 1
	}
	body := []byte{byte(channel)}
	body = append(body, bcdTimeFromUTC(startUTC, tz)...)
	body = append(body, bcdTimeFromUTC(endUTC, tz)...)
	// Alarm flag (8 bytes) is a FILTER, not a selector: the device returns only
	// files whose 0x0200 alarm bits match the ones set here. All-0xFF asks for a
	// clip that raised every alarm at once, so ordinary continuous recordings
	// (alarm bits = 0) are excluded and the list comes back empty. Zero = "no
	// alarm filter", i.e. all footage; callers classify/filter per-file afterward.
	body = append(body, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // alarm filter: none (all footage)
	body = append(body, 0x00, 0x00, 0x00)                               // resource A/V, stream all, memory all

	s.log().Debug(map[string]any{
		"event": "query_recordings", "serial": s.serial, "channel": channel,
		"start_utc": startUTC, "end_utc": endUTC, "tz_offset": tz,
	})
	resp, err := s.request(ctx, msgResourceList, buildFrame(msgResourceQuery, s.phone, s.nextPlatSerial(), body))
	if err != nil {
		return nil, err
	}
	recs, _ := resp["recordings"].([]gateway.Recording)
	if recs == nil {
		recs = []gateway.Recording{}
	}
	return recs, nil
}

// parseResourceList decodes a 0x1205 body into recordings: reply serial(2) +
// total(4) + N×28-byte entries.
func parseResourceList(body []byte, offsetHours float64) []gateway.Recording {
	out := []gateway.Recording{}
	const entry = 28
	for off := 6; off+entry <= len(body); off += entry {
		e := body[off : off+entry]
		channel := int(e[0])
		startUTC := parseBCDTime(e[1:7], offsetHours)
		endUTC := parseBCDTime(e[7:13], offsetHours)
		alarm := e[13:21] // 8-byte alarm bitfield for this file
		streamType := int(e[22])
		size := int64(binary.BigEndian.Uint32(e[24:28]))
		camera := 0
		if channel > 0 {
			camera = channel - 1
		}
		profile := 0
		if streamType == 2 { // 2 = sub
			profile = 1
		}
		isAlarm := false
		alarmHex := ""
		for _, b := range alarm {
			if b != 0 {
				isAlarm = true
				alarmHex = hex.EncodeToString(alarm)
				break
			}
		}
		out = append(out, gateway.Recording{
			Camera:      camera,
			Profile:     profile,
			StartUTC:    startUTC,
			EndUTC:      endUTC,
			Size:        size,
			DeviceStart: localTimeString(startUTC, offsetHours),
			DeviceEnd:   localTimeString(endUTC, offsetHours),
			Alarm:       isAlarm,
			AlarmFlags:  alarmHex,
		})
	}
	return out
}

// localTimeString renders a UTC epoch as the device's local wall-clock for
// diagnostics.
func localTimeString(unix int64, offsetHours float64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).UTC().Add(time.Duration(offsetHours * float64(time.Hour)))
	return t.Format("2006-01-02 15:04:05")
}
