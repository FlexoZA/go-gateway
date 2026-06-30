package jt808

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// recordings.go implements the recorded-footage file query: platform sends a
// resource-list query (0x9205) and the device replies with the list (0x1205).

// QueryRecordings asks the device what footage it has for a camera/time window.
// camera < 0 queries all channels. profile is advisory; we query all stream types
// and report each entry's own stream type.
func (s *session) QueryRecordings(ctx context.Context, camera, profile int, startUTC, endUTC int64) ([]gateway.Recording, error) {
	if !s.approved {
		return nil, gateway.ErrNotConnected
	}
	channel := 0
	if camera >= 0 {
		channel = camera + 1
	}
	tz := s.conn.Deps.DeviceTZOffsetHours

	body := []byte{byte(channel)}
	body = append(body, bcdTimeFromUTC(startUTC, tz)...)
	body = append(body, bcdTimeFromUTC(endUTC, tz)...)
	body = append(body, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff) // alarm sign: all
	body = append(body, 0x00, 0x00, 0x00)                               // resource A/V, stream all, memory all

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
		out = append(out, gateway.Recording{
			Camera:      camera,
			Profile:     profile,
			StartUTC:    startUTC,
			EndUTC:      endUTC,
			Size:        size,
			DeviceStart: localTimeString(startUTC, offsetHours),
			DeviceEnd:   localTimeString(endUTC, offsetHours),
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
