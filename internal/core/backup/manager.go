// Package backup manages on-disk gateway-database backups: it writes a compressed
// logical dump (see postgres.Store.DumpTo) to a backups directory, lists them,
// serves them for download, prunes old ones, and deletes on request. It holds no
// schedule of its own — the app's scheduler calls RunBackup/Prune on a cadence read
// from admin settings.
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// Dumper writes a database backup archive to w. Implemented by *postgres.Store.
type Dumper interface {
	DumpTo(ctx context.Context, w io.Writer, createdAt time.Time) (int64, error)
}

const (
	filePrefix = "gateway-backup-"
	fileSuffix = ".tar.gz"
	tsLayout   = "20060102T150405Z"
)

// Info describes one backup archive on disk.
type Info struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Rows      int64     `json:"rows,omitempty"` // only known for a just-run backup
}

// Manager owns the backups directory.
type Manager struct {
	dir   string
	store Dumper
	log   *logging.Logger
}

// NewManager returns a backup manager writing to dir (created if missing).
func NewManager(dir string, store Dumper, log *logging.Logger) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("backup: mkdir %s: %w", dir, err)
	}
	return &Manager{dir: dir, store: store, log: log.With("backup")}, nil
}

// RunBackup writes a new backup archive and returns its info. It writes to a temp
// file first and renames on success so a crash mid-dump never leaves a truncated
// archive that looks valid.
func (m *Manager) RunBackup(ctx context.Context, now time.Time) (Info, error) {
	name := filePrefix + now.UTC().Format(tsLayout) + fileSuffix
	final := filepath.Join(m.dir, name)
	tmp := final + ".partial"

	f, err := os.Create(tmp)
	if err != nil {
		return Info{}, fmt.Errorf("backup create: %w", err)
	}
	rows, dumpErr := m.store.DumpTo(ctx, f, now)
	closeErr := f.Close()
	if dumpErr != nil {
		_ = os.Remove(tmp)
		return Info{}, fmt.Errorf("backup dump: %w", dumpErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return Info{}, fmt.Errorf("backup close: %w", closeErr)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return Info{}, fmt.Errorf("backup rename: %w", err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return Info{}, err
	}
	m.log.Info(map[string]any{"event": "backup_created", "name": name, "bytes": fi.Size(), "rows": rows})
	return Info{Name: name, Size: fi.Size(), CreatedAt: now.UTC(), Rows: rows}, nil
}

// List returns existing backups, newest first.
func (m *Manager) List() ([]Info, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Info{}, nil
		}
		return nil, err
	}
	out := []Info{}
	for _, e := range entries {
		if e.IsDir() || !validName(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Name: e.Name(), Size: fi.Size(), CreatedAt: parseStamp(e.Name(), fi.ModTime())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Open returns a reader and size for a backup by name (for download). The name is
// validated so it can only reference a file in the backups directory.
func (m *Manager) Open(name string) (io.ReadCloser, int64, error) {
	p, err := m.pathFor(name)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

// Delete removes a backup by name.
func (m *Manager) Delete(name string) error {
	p, err := m.pathFor(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Prune keeps the newest `keep` backups and deletes the rest, returning how many
// were removed. keep <= 0 disables pruning (keep all).
func (m *Manager) Prune(keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	list, err := m.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, b := range list[min(keep, len(list)):] {
		if err := m.Delete(b.Name); err != nil {
			m.log.Debug(map[string]any{"event": "backup_prune_failed", "name": b.Name, "error": err.Error()})
			continue
		}
		removed++
	}
	if removed > 0 {
		m.log.Info(map[string]any{"event": "backups_pruned", "removed": removed, "kept": keep})
	}
	return removed, nil
}

// pathFor validates name and returns its absolute path inside the backups dir.
func (m *Manager) pathFor(name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("backup: invalid name %q", name)
	}
	return filepath.Join(m.dir, name), nil
}

// validName guards against path traversal: a backup name is exactly our generated
// shape, with no separators.
func validName(name string) bool {
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return strings.HasPrefix(name, filePrefix) && strings.HasSuffix(name, fileSuffix)
}

// parseStamp recovers the backup time from its filename, falling back to file mtime.
func parseStamp(name string, mod time.Time) time.Time {
	s := strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix)
	if t, err := time.Parse(tsLayout, s); err == nil {
		return t.UTC()
	}
	return mod.UTC()
}
