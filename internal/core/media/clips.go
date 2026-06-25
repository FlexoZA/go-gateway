package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// ClipStore is the subset of the database the clip pipeline writes to as frames
// arrive. The postgres store implements it.
type ClipStore interface {
	CreateClip(ctx context.Context, serial string, camera, profile int, startUtc, endUtc int64, storagePath string) (int64, error)
	UpdateClipStatus(ctx context.Context, id int64, status, errMsg string) error
	UpdateClipProgress(ctx context.Context, id, bytesReceived, fileSize int64) error
	FinishClip(ctx context.Context, id, fileSize int64) error
}

// clipProgressBytes is how many bytes must accumulate before a DB progress write.
const clipProgressBytes = 256 * 1024

// ClipRegistry tracks in-flight clip downloads. A clip is requested on the
// control connection (NewClip), then the device dials the media port and streams
// recorded frames (WriteFrame), which are remuxed to an .mp4 and finalized on
// PLAYBACK_END or when the media connection closes (Finish).
type ClipRegistry struct {
	mgr   *Manager
	store ClipStore
	root  string
	log   *logging.Logger

	mu   sync.Mutex
	byID map[string]*clipSession
}

type clipSession struct {
	ss      string
	clipID  int64
	serial  string
	camera  int
	profile int
	outFile string

	mu        sync.Mutex
	started   bool     // ffmpeg writer running (begins on the first keyframe)
	raw       *os.File // raw-container writer (set for clips delivered as a finished file, e.g. MP4)
	bytes     int64
	lastProg  int64
	finalized bool
}

// NewClipRegistry constructs a clip registry writing .mp4 files under root.
func NewClipRegistry(mgr *Manager, store ClipStore, root string, log *logging.Logger) *ClipRegistry {
	return &ClipRegistry{mgr: mgr, store: store, root: root, log: log.With("clip"), byID: map[string]*clipSession{}}
}

// Root is the directory clip .mp4 files are stored under.
func (r *ClipRegistry) Root() string { return r.root }

func dbctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// clipStoragePath is the .mp4 location relative to the bucket root.
func clipStoragePath(serial string, camera, profile int, startUtc, endUtc int64) string {
	return filepath.Join(serial, fmt.Sprintf("camera%d_profile%d_%d_%d.mp4", camera, profile, startUtc, endUtc))
}

// NewClip creates the DB record and registers an in-flight session, returning
// the clip id and the session id (ss) to put in the playback request. The device
// will echo ss when it dials the media port.
func (r *ClipRegistry) NewClip(ctx context.Context, serial string, camera, profile int, startUtc, endUtc int64) (int64, string, error) {
	storagePath := clipStoragePath(serial, camera, profile, startUtc, endUtc)
	clipID, err := r.store.CreateClip(ctx, serial, camera, profile, startUtc, endUtc, storagePath)
	if err != nil {
		return 0, "", err
	}
	ss := fmt.Sprintf("clip_%s_%d_%d_%d", serial, camera, profile, startUtc)
	r.mu.Lock()
	r.byID[ss] = &clipSession{
		ss: ss, clipID: clipID, serial: serial, camera: camera, profile: profile,
		outFile: filepath.Join(r.root, storagePath),
	}
	r.mu.Unlock()
	return clipID, ss, nil
}

// IsClip reports whether ss belongs to an in-flight clip (vs a live stream).
func (r *ClipRegistry) IsClip(ss string) bool {
	r.mu.Lock()
	_, ok := r.byID[ss]
	r.mu.Unlock()
	return ok
}

func (r *ClipRegistry) get(ss string) *clipSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[ss]
}

// WriteFrame feeds one recorded video access-unit to a clip's writer. Frames
// before the first keyframe are dropped so the .mp4 starts cleanly; the input
// codec is detected from the first keyframe.
func (r *ClipRegistry) WriteFrame(ss string, isKeyframe bool, data []byte) {
	s := r.get(ss)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return
	}
	if !s.started {
		if !isKeyframe {
			return // wait for a keyframe before opening the file
		}
		codec := detectCodec(data)
		if _, err := r.mgr.RegisterClip(ss, s.serial, s.camera, s.profile, s.outFile, codec); err != nil {
			r.log.Error(map[string]any{"event": "clip_register_failed", "ss": ss, "error": err.Error()})
			return
		}
		s.started = true
		ctx, cancel := dbctx()
		_ = r.store.UpdateClipStatus(ctx, s.clipID, "receiving", "")
		cancel()
		r.log.Info(map[string]any{"event": "clip_receiving", "ss": ss, "clip_id": s.clipID, "codec": codec})
	}
	if err := r.mgr.WriteVideo(ss, data); err != nil {
		return
	}
	s.bytes += int64(len(data))
	if s.bytes-s.lastProg >= clipProgressBytes {
		s.lastProg = s.bytes
		ctx, cancel := dbctx()
		_ = r.store.UpdateClipProgress(ctx, s.clipID, s.bytes, 0)
		cancel()
	}
}

// Finish finalizes a clip: it closes ffmpeg, records the file size, and marks the
// clip ready. Idempotent — the first of PLAYBACK_END or media-connection-close
// wins; later calls are no-ops.
func (r *ClipRegistry) Finish(ss string) {
	r.mu.Lock()
	s := r.byID[ss]
	delete(r.byID, ss)
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	s.finalized = true
	started := s.started
	clipID := s.clipID
	s.mu.Unlock()

	ctx, cancel := dbctx()
	defer cancel()
	if !started {
		r.log.Info(map[string]any{"event": "clip_empty", "ss": ss, "clip_id": clipID})
		_ = r.store.UpdateClipStatus(ctx, clipID, "error", "device sent no video")
		return
	}
	size, err := r.mgr.FinishClip(ss)
	if err != nil || size == 0 {
		msg := "clip file empty"
		if err != nil {
			msg = err.Error()
		}
		r.log.Error(map[string]any{"event": "clip_finalize_failed", "ss": ss, "clip_id": clipID, "error": msg})
		_ = r.store.UpdateClipStatus(ctx, clipID, "error", msg)
		return
	}
	r.log.Info(map[string]any{"event": "clip_ready", "ss": ss, "clip_id": clipID, "bytes": size})
	_ = r.store.FinishClip(ctx, clipID, size)
}

// Abort cancels a clip before/around the transfer (e.g. the device rejected the
// playback request): it tears down any writer and marks the clip errored.
func (r *ClipRegistry) Abort(ss, reason string) {
	r.mu.Lock()
	s := r.byID[ss]
	delete(r.byID, ss)
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	s.finalized = true
	clipID := s.clipID
	s.mu.Unlock()

	if s.raw != nil { // raw-file clip: close and drop the partial
		_ = s.raw.Close()
		_ = os.Remove(s.outFile)
	} else {
		r.mgr.Stop(ss) // kills ffmpeg and removes the partial file
	}
	ctx, cancel := dbctx()
	_ = r.store.UpdateClipStatus(ctx, clipID, "error", reason)
	cancel()
}

// WriteRaw feeds bytes of a clip delivered as a finished container (e.g. an MP4
// the device uploads whole, as Cathexis does) straight to the output file — no
// ffmpeg, no keyframe gating. Pair with FinishRaw. The first call opens the file.
func (r *ClipRegistry) WriteRaw(ss string, data []byte) {
	s := r.get(ss)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return
	}
	if !s.started {
		if err := os.MkdirAll(filepath.Dir(s.outFile), 0o755); err != nil {
			r.log.Error(map[string]any{"event": "clip_mkdir_failed", "ss": ss, "error": err.Error()})
			return
		}
		f, err := os.Create(s.outFile)
		if err != nil {
			r.log.Error(map[string]any{"event": "clip_create_failed", "ss": ss, "error": err.Error()})
			return
		}
		s.raw = f
		s.started = true
		ctx, cancel := dbctx()
		_ = r.store.UpdateClipStatus(ctx, s.clipID, "receiving", "")
		cancel()
		r.log.Info(map[string]any{"event": "clip_receiving", "ss": ss, "clip_id": s.clipID, "mode": "raw"})
	}
	if _, err := s.raw.Write(data); err != nil {
		r.log.Debug(map[string]any{"event": "clip_raw_write_failed", "ss": ss, "error": err.Error()})
		return
	}
	s.bytes += int64(len(data))
	if s.bytes-s.lastProg >= clipProgressBytes {
		s.lastProg = s.bytes
		ctx, cancel := dbctx()
		_ = r.store.UpdateClipProgress(ctx, s.clipID, s.bytes, 0)
		cancel()
	}
}

// FinishRaw finalizes a raw-file clip: closes the file, records its size, and
// marks the clip ready. Idempotent.
func (r *ClipRegistry) FinishRaw(ss string) {
	r.mu.Lock()
	s := r.byID[ss]
	delete(r.byID, ss)
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	s.finalized = true
	started := s.started
	clipID := s.clipID
	size := s.bytes
	if s.raw != nil {
		_ = s.raw.Close()
	}
	outFile := s.outFile
	s.mu.Unlock()

	ctx, cancel := dbctx()
	defer cancel()
	if !started || size == 0 {
		r.log.Info(map[string]any{"event": "clip_empty", "ss": ss, "clip_id": clipID})
		_ = r.store.UpdateClipStatus(ctx, clipID, "error", "device sent no data")
		_ = os.Remove(outFile)
		return
	}
	r.log.Info(map[string]any{"event": "clip_ready", "ss": ss, "clip_id": clipID, "bytes": size, "mode": "raw"})
	_ = r.store.FinishClip(ctx, clipID, size)
}

// detectCodec inspects an Annex-B access unit to decide whether it is H.264 or
// H.265/HEVC, by reading the NAL type after the first start code.
func detectCodec(data []byte) string {
	if len(data) < 5 {
		return "h264"
	}
	off := -1
	for i := 0; i+3 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			off = i + 3
			break
		}
		if i+4 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			off = i + 4
			break
		}
	}
	if off < 0 || off >= len(data) {
		return "h264"
	}
	b := data[off]
	h265Type := (b >> 1) & 0x3f
	switch {
	case b == 0x40, b == 0x42, b == 0x44, b == 0x4e, b == 0x26, b == 0x02,
		h265Type == 32, h265Type == 33, h265Type == 34, h265Type == 39, h265Type == 19, h265Type == 20:
		return "hevc"
	}
	return "h264"
}
