package howen

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// RequestSnapshot asks the device to capture a still image on the given camera
// channels and waits for the 0x1020 response (H-Protocol §2.5). It is a
// signal-link request (no media connection), so it works even when live video
// is not enabled on the gateway.
//
// `channels` are 0-based gateway camera indexes (the H-Protocol channel is
// camera+1). `resolution`: 0 follow-video, 1 1080, 2 720, 3 VGA, 4 D1.
//
// The response reports the device-side file paths (rl[].fn); it does NOT carry
// the image bytes. Retrieving the JPEG needs the file-transfer path (0x4090),
// which is a later milestone — see docs/Howen_mapping_improvements.md §6.
func (s *session) RequestSnapshot(ctx context.Context, channels []int, resolution int) (gateway.SnapshotResult, error) {
	if s.gate != gateApproved {
		return gateway.SnapshotResult{}, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return gateway.SnapshotResult{}, gateway.ErrDeviceSleeping
	}
	if len(channels) == 0 {
		channels = []int{0}
	}
	parts := make([]string, 0, len(channels))
	for _, c := range channels {
		if c < 0 {
			return gateway.SnapshotResult{}, fmt.Errorf("invalid camera index %d", c)
		}
		parts = append(parts, strconv.Itoa(c+1)) // gateway 0-based -> H-Protocol 1-based
	}
	body := map[string]any{"cl": strings.Join(parts, ";")}
	if resolution > 0 {
		body["res"] = strconv.Itoa(resolution)
	}

	ss := fmt.Sprintf("snap_%s_%s", s.serial, hexNow())
	ch := make(chan map[string]any, 1)
	s.pendingMu.Lock()
	s.pending[ss] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, ss)
		s.pendingMu.Unlock()
	}()

	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgSnapshot, mergeBody(ss, body))); err != nil {
		return gateway.SnapshotResult{}, err
	}

	select {
	case <-ctx.Done():
		return gateway.SnapshotResult{}, gateway.ErrCommandTimeout
	case resp := <-ch:
		if code := strings.TrimSpace(toString(resp["err"])); code != "" && code != "0" {
			return gateway.SnapshotResult{}, fmt.Errorf("device rejected snapshot: err=%s", describeHowenError(code))
		}
		return gateway.SnapshotResult{SessionID: ss, Files: parseSnapshotResult(resp)}, nil
	}
}

// CaptureImage captures a still on one camera and fetches the JPEG bytes back via
// the file-transfer path (0x4020 capture -> 0x1020 paths -> 0x4090 download). It
// requires the gateway media port/advertise host (the device dials it to deliver
// the bytes). Returns the raw JPEG.
func (s *session) CaptureImage(ctx context.Context, camera, resolution int) ([]byte, error) {
	if s.conn.Deps.Snapshots == nil || s.conn.Deps.MediaAdvertiseHost == "" {
		return nil, errors.New("snapshot image fetch is not enabled on this gateway")
	}
	res, err := s.RequestSnapshot(ctx, []int{camera}, resolution)
	if err != nil {
		return nil, err
	}
	if len(res.Files) == 0 {
		return nil, errors.New("device returned no snapshot file")
	}
	return s.fetchDeviceFile(ctx, res.Files[0].DevicePath, "3") // ft=3: general snapshot
}

// fetchDeviceFile downloads a device-side file (by path) over the file-transfer
// path: it registers an in-memory fetch, sends 0x4090 (act=0, download from
// device), waits for the 0x1090 ack, then for the media bytes the device streams
// to the media port. ft is the H-Protocol file-type code (§3.4).
func (s *session) fetchDeviceFile(ctx context.Context, path, ft string) ([]byte, error) {
	ss := fmt.Sprintf("ft_%s_%s", s.serial, hexNow())
	done := s.conn.Deps.Snapshots.Begin(ss)
	defer s.conn.Deps.Snapshots.Abort(ss) // no-op once Finish has run

	ack := make(chan map[string]any, 1)
	s.pendingMu.Lock()
	s.pending[ss] = ack
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, ss)
		s.pendingMu.Unlock()
	}()

	body := map[string]any{
		"act": "0", // download from device to server
		"srv": s.conn.Deps.MediaAdvertiseHost,
		"ft":  ft,
		"fn":  path,
		"fo":  "0",
	}
	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgFileTransfer, mergeBody(ss, body))); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case resp := <-ack:
		if code := strings.TrimSpace(toString(resp["err"])); code != "" && code != "0" {
			return nil, fmt.Errorf("device rejected file transfer: err=%s", describeHowenError(code))
		}
	}

	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case data := <-done:
		if len(data) == 0 {
			return nil, errors.New("device sent no file data")
		}
		return data, nil
	}
}

// parseSnapshotResult extracts the rl[] {ch, fn} entries from a 0x1020 response.
func parseSnapshotResult(resp map[string]any) []gateway.SnapshotFile {
	raw, ok := resp["rl"].([]any)
	if !ok {
		return nil
	}
	files := make([]gateway.SnapshotFile, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn := strings.TrimSpace(toString(m["fn"]))
		if fn == "" {
			continue
		}
		chNum, _ := numberOrNullInt(m["ch"])
		files = append(files, gateway.SnapshotFile{Channel: chNum, DevicePath: fn})
	}
	return files
}
