package jt808

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// snapshot.go implements gateway.Snapshotter for the N62: an on-demand still is
// requested with a camera-immediate-shoot command (0x8801) and the device uploads
// the JPEG over the control connection via one or more multimedia-data messages
// (0x0801), which may be subpackaged. The image is reassembled and either handed
// to a waiting CaptureImage call or (when pushed unsolicited) saved to the bucket.

// multimediaReasm reassembles a subpackaged 0x0801 upload.
type multimediaReasm struct {
	total uint16
	bytes int // aggregate size of buffered fragments, bounded by maxReasmBytes
	parts map[uint16][]byte
}

// CaptureImage triggers a capture on one camera and returns the JPEG inline.
func (s *session) CaptureImage(ctx context.Context, camera, resolution int) ([]byte, error) {
	if !s.approved {
		return nil, gateway.ErrNotConnected
	}
	if camera < 0 {
		return nil, errors.New("invalid camera")
	}
	ch := make(chan []byte, 1)
	s.snapMu.Lock()
	if s.snapWaiter != nil {
		s.snapMu.Unlock()
		return nil, errors.New("another capture is already in progress")
	}
	s.snapWaiter = ch
	s.snapReasm = nil
	s.snapMu.Unlock()
	defer func() {
		s.snapMu.Lock()
		s.snapWaiter = nil
		s.snapReasm = nil
		s.snapMu.Unlock()
	}()

	channel := camera + 1
	body := buildCameraShoot(channel, 1, resolution, 1)
	if err := s.conn.WriteFrame(buildFrame(msgCameraShoot, s.phone, s.nextPlatSerial(), body)); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case img := <-ch:
		if len(img) == 0 {
			return nil, errors.New("device returned no image")
		}
		return img, nil
	}
}

// RequestSnapshot captures the given channels (default camera 0), saves each JPEG
// to the gateway bucket, and returns the per-channel references.
func (s *session) RequestSnapshot(ctx context.Context, channels []int, resolution int) (gateway.SnapshotResult, error) {
	if len(channels) == 0 {
		channels = []int{0}
	}
	res := gateway.SnapshotResult{SessionID: fmt.Sprintf("snap_%s", s.serial)}
	for _, cam := range channels {
		img, err := s.CaptureImage(ctx, cam, resolution)
		if err != nil {
			s.log().Debug(map[string]any{"event": "snapshot_capture_failed", "serial": s.serial, "camera": cam, "error": err.Error()})
			continue
		}
		path := s.saveSnapshot(cam, img, "on_demand")
		res.Files = append(res.Files, gateway.SnapshotFile{Channel: cam, DevicePath: path})
	}
	if len(res.Files) == 0 {
		return res, errors.New("no image captured")
	}
	return res, nil
}

// SearchSnapshots is not supported: the N62 has no device-side stored-image query
// (stills are pushed, not indexed). Returns an empty list so the UI shows "none".
func (s *session) SearchSnapshots(ctx context.Context, camera int, startUTC, endUTC int64, kind string) ([]gateway.SnapshotFileInfo, error) {
	return []gateway.SnapshotFileInfo{}, nil
}

// FetchSnapshotFile is not supported: JT808 stills arrive inline (no device path
// to fetch by). Use CaptureImage / the saved-snapshots store instead.
func (s *session) FetchSnapshotFile(ctx context.Context, devicePath string) ([]byte, error) {
	return nil, errors.New("fetch-by-path is not supported on JT808; stills upload inline")
}

// handleMultimediaData processes a 0x0801 upload: it reassembles subpackages,
// extracts the JPEG, and delivers it to a waiting CaptureImage call (or saves it
// when the upload was unsolicited).
func (s *session) handleMultimediaData(h header, body []byte) {
	full, complete := s.reassembleMultimedia(h, body)
	if !complete {
		return
	}
	jpeg := extractJPEG(full)
	if jpeg == nil {
		s.log().Debug(map[string]any{"event": "multimedia_no_jpeg", "serial": s.serial, "len": len(full)})
		return
	}
	camera := 0
	if len(full) >= 8 && full[7] > 0 {
		camera = int(full[7]) - 1 // 0x0801 channel byte (1-based)
	}

	s.snapMu.Lock()
	w := s.snapWaiter
	s.snapMu.Unlock()
	if w != nil {
		select {
		case w <- jpeg:
		default:
		}
		return
	}
	// Unsolicited push (e.g. event/interval capture): persist it.
	s.saveSnapshot(camera, jpeg, "device_push")
}

// reassembleMultimedia returns the full 0x0801 logical body. For a single-packet
// upload it is the body as-is; for a subpackaged one it accumulates packets by
// 1-based index until all are present.
func (s *session) reassembleMultimedia(h header, body []byte) ([]byte, bool) {
	if !h.Subpackage || h.SubTotal <= 1 {
		return body, true
	}
	// SubIndex is 1-based and must fall within the advertised total; fragment count
	// and size are device-controlled, so the aggregate is capped at maxReasmBytes.
	if h.SubIndex < 1 || h.SubIndex > h.SubTotal {
		return nil, false
	}
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.snapReasm == nil || s.snapReasm.total != h.SubTotal || h.SubIndex == 1 {
		s.snapReasm = &multimediaReasm{total: h.SubTotal, parts: map[uint16][]byte{}}
	}
	if prev, ok := s.snapReasm.parts[h.SubIndex]; ok {
		s.snapReasm.bytes -= len(prev) // a resent fragment replaces the old one
	}
	if s.snapReasm.bytes+len(body) > maxReasmBytes {
		s.snapReasm = nil
		s.log().Debug(map[string]any{"event": "multimedia_oversize", "serial": s.serial, "total": h.SubTotal})
		return nil, false
	}
	s.snapReasm.parts[h.SubIndex] = append([]byte(nil), body...)
	s.snapReasm.bytes += len(body)
	if len(s.snapReasm.parts) < int(h.SubTotal) {
		return nil, false
	}
	full := make([]byte, 0, s.snapReasm.bytes)
	for i := uint16(1); i <= h.SubTotal; i++ {
		full = append(full, s.snapReasm.parts[i]...)
	}
	s.snapReasm = nil
	return full, true
}

// saveSnapshot persists a JPEG to the gateway bucket (best-effort, off the read
// loop) and returns a reference string for the API result.
func (s *session) saveSnapshot(camera int, jpeg []byte, source string) string {
	saver := s.conn.Deps.SnapshotSaver
	if saver == nil {
		return ""
	}
	serial := s.serial
	data := append([]byte(nil), jpeg...)
	utc := time.Now().Unix()
	ref := fmt.Sprintf("%s/snapshots/%d_%d.jpg", serial, camera, utc)
	go func() {
		defer func() { _ = recover() }()
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		id, err := saver.Save(sctx, serial, camera, "general", source, utc, data)
		if err != nil {
			s.log().Error(map[string]any{"event": "snapshot_save_failed", "serial": serial, "camera": camera, "error": err.Error()})
			return
		}
		s.log().Info(map[string]any{"event": "snapshot_saved", "serial": serial, "camera": camera, "id": id, "bytes": len(data)})
	}()
	return ref
}

// extractJPEG returns the JPEG bounded by its SOI (0xFFD8) and EOI (0xFFD9)
// markers within a reassembled multimedia body, tolerant of any leading header.
func extractJPEG(b []byte) []byte {
	start := bytes.Index(b, []byte{0xff, 0xd8})
	if start < 0 {
		return nil
	}
	if end := bytes.LastIndex(b, []byte{0xff, 0xd9}); end > start {
		return b[start : end+2]
	}
	return b[start:]
}
