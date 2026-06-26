package howen

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// recordings.go implements file query (H-Protocol 0x4060 → 0x1060): asking the
// device what footage it has on its SD card for a camera/time window. The device
// streams one response frame per file (err=8, the file in `fi`), terminated by a
// final frame (err=9). This is the canonical way to discover playable windows —
// Howen playback (0x4070) only matches files the device actually has.

// fileQueryCollector accumulates the multi-frame 0x1060 response for one query.
type fileQueryCollector struct {
	mu    sync.Mutex
	files []map[string]any
	err   error
	done  chan struct{}
	once  sync.Once
}

func (c *fileQueryCollector) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

// collectFileQuery routes a 0x1060 frame to the waiting QueryRecordings by ss.
func (s *session) collectFileQuery(obj map[string]any) {
	ss := toString(obj["ss"])
	if ss == "" {
		return
	}
	s.pendingMu.Lock()
	c := s.fileQueries[ss]
	s.pendingMu.Unlock()
	if c == nil {
		return
	}
	switch strings.TrimSpace(toString(obj["err"])) {
	case "8": // a file entry
		if fi, ok := obj["fi"].(map[string]any); ok {
			c.mu.Lock()
			c.files = append(c.files, fi)
			c.mu.Unlock()
		}
	case "9": // end of list
		c.finish(nil)
	default:
		c.finish(fmt.Errorf("device rejected file query: err=%s", describeHowenError(toString(obj["err"]))))
	}
}

// QueryRecordings lists the device's recordings for a camera (or all cameras when
// camera < 0) over a UTC time window. The window is localized to the device clock
// for the request and the returned times are converted back to UTC.
func (s *session) QueryRecordings(ctx context.Context, camera, profile int, startUTC, endUTC int64) ([]gateway.Recording, error) {
	if s.gate != gateApproved {
		return nil, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return nil, gateway.ErrDeviceSleeping
	}
	if profile < 0 || profile > 1 {
		return nil, errors.New("invalid profile")
	}
	if startUTC <= 0 || endUTC <= startUTC {
		return nil, errors.New("invalid start_utc / end_utc")
	}

	channel := "0" // 0 = all cameras
	if camera >= 0 {
		channel = strconv.Itoa(camera + 1)
	}
	_, streamType := channelStream(0, profile)
	tz := s.conn.Deps.DeviceTZOffsetHours

	files, err := s.queryFiles(ctx, channel, streamType, startUTC, endUTC, "1") // ft 1 = normal recording
	if err != nil {
		return nil, err
	}
	return parseRecordings(files, profile, tz), nil
}

// queryFiles runs one file query (0x4060) for a file type and returns the raw
// device file entries (the `fi` objects). Shared by QueryRecordings (ft=1) and
// SearchSnapshots (ft=3/4). The caller localizes/parses times.
func (s *session) queryFiles(ctx context.Context, channel string, streamType int, startUTC, endUTC int64, ft string) ([]map[string]any, error) {
	tz := s.conn.Deps.DeviceTZOffsetHours

	ss := fmt.Sprintf("query_%s_%s", s.serial, hexNow())
	c := &fileQueryCollector{done: make(chan struct{})}
	s.pendingMu.Lock()
	s.fileQueries[ss] = c
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.fileQueries, ss)
		s.pendingMu.Unlock()
	}()

	body := map[string]any{
		"ss":  ss,
		"chl": channel,
		"st":  formatHowenDeviceTime(startUTC, tz),
		"et":  formatHowenDeviceTime(endUTC, tz),
		"ft":  ft,
		"si":  strconv.Itoa(streamType),
	}
	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgFileQuery, body)); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case <-c.done:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return c.files, nil
}

// parseRecordings converts raw device file entries to gateway.Recording, mapping
// device-local wall-clock st/et back to UTC epochs (the inverse of the offset
// applied on the request).
func parseRecordings(files []map[string]any, profile int, tzOffset float64) []gateway.Recording {
	out := make([]gateway.Recording, 0, len(files))
	for _, fi := range files {
		st := toString(fi["st"])
		et := toString(fi["et"])
		cam := int(recInt(fi["chl"])) - 1
		if cam < 0 {
			cam = 0
		}
		out = append(out, gateway.Recording{
			Camera:      cam,
			Profile:     profile,
			StartUTC:    parseHowenDeviceTime(st, tzOffset),
			EndUTC:      parseHowenDeviceTime(et, tzOffset),
			FileName:    toString(fi["fn"]),
			Size:        recInt(fi["fs"]),
			DeviceStart: st,
			DeviceEnd:   et,
		})
	}
	return out
}

// recInt coerces a device JSON value (string or number) to int64.
func recInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	}
	return 0
}

// parseHowenDeviceTime parses a device-local wall-clock string ("YYYY-MM-DD
// HH:MM:SS", offsetHours from UTC) back to a true UTC unix time. Returns 0 if
// unparseable.
func parseHowenDeviceTime(s string, offsetHours float64) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return 0
	}
	return t.Add(-time.Duration(offsetHours * float64(time.Hour))).Unix()
}
