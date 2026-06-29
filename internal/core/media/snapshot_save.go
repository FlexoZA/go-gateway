package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// SnapshotStore is the subset of the database the snapshot saver writes to.
// Kept primitive so the storage layer (*postgres.Store) satisfies it structurally
// without importing this package.
type SnapshotStore interface {
	CreateSnapshot(ctx context.Context, serial string, camera int, kind, source string, capturedUTC int64, devicePath, storagePath string, fileSize int64) (int64, error)
}

// SnapshotSaver persists device-pushed JPEG stills (e.g. Cathexis event-preview
// snapshots) to the server bucket: the bytes are written under
// root/snapshots/<serial>/ and a row is recorded in the snapshots table. It
// mirrors the HTTP snapshot-save path but is server-initiated (the device pushes
// the image unsolicited), so it lives in core where a unit session can reach it
// via Deps.
type SnapshotSaver struct {
	store SnapshotStore
	root  string
	log   *logging.Logger
}

// NewSnapshotSaver constructs a saver writing under root. store and root must be
// set for Save to succeed.
func NewSnapshotSaver(store SnapshotStore, root string, log *logging.Logger) *SnapshotSaver {
	return &SnapshotSaver{store: store, root: root, log: log.With("media/snapshot-saver")}
}

// Save writes one JPEG and records its metadata, returning the new row id. camera
// is the channel (0=road, 1=cab); kind is a free label (the event name for
// event-preview snapshots); source identifies the origin (e.g. "event").
func (s *SnapshotSaver) Save(ctx context.Context, serial string, camera int, kind, source string, capturedUTC int64, jpeg []byte) (int64, error) {
	if s == nil || s.store == nil || s.root == "" {
		return 0, fmt.Errorf("snapshot storage not configured")
	}
	rel := filepath.ToSlash(filepath.Join("snapshots", serial, fmt.Sprintf("snap_%d_cam%d.jpg", time.Now().UnixNano(), camera)))
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, fmt.Errorf("create snapshot dir: %w", err)
	}
	if err := os.WriteFile(full, jpeg, 0o644); err != nil {
		return 0, fmt.Errorf("write snapshot: %w", err)
	}
	id, err := s.store.CreateSnapshot(ctx, serial, camera, kind, source, capturedUTC, "", rel, int64(len(jpeg)))
	if err != nil {
		_ = os.Remove(full)
		return 0, err
	}
	return id, nil
}
