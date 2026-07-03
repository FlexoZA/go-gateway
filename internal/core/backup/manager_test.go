package backup

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// fakeDumper writes fixed bytes and reports a row count, standing in for the store.
type fakeDumper struct{ payload string }

func (f fakeDumper) DumpTo(_ context.Context, w io.Writer, _ time.Time) (int64, error) {
	n, err := io.WriteString(w, f.payload)
	return int64(n), err
}

func mgr(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), fakeDumper{payload: "BACKUP-BYTES"}, logging.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRunListOpenPrune(t *testing.T) {
	m := mgr(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 3, 2, 0, 0, 0, time.UTC)

	// Three backups at distinct timestamps.
	for i := 0; i < 3; i++ {
		if _, err := m.RunBackup(ctx, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("List = %d, want 3", len(list))
	}
	// Newest first.
	if !list[0].CreatedAt.After(list[1].CreatedAt) {
		t.Fatal("List not sorted newest-first")
	}

	// Open the newest and read its bytes back.
	rc, size, err := m.Open(list[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if size <= 0 {
		t.Fatalf("Open size = %d, want > 0", size)
	}

	// Prune to 1 keeps the newest, deletes the two older.
	removed, err := m.Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("Prune removed %d, want 2", removed)
	}
	if list, _ := m.List(); len(list) != 1 {
		t.Fatalf("after prune List = %d, want 1", len(list))
	}
}

func TestOpenRejectsTraversal(t *testing.T) {
	m := mgr(t)
	for _, bad := range []string{"../etc/passwd", "gateway-backup-../x.tar.gz", "notabackup.txt", "/abs/path"} {
		if _, _, err := m.Open(bad); err == nil {
			t.Fatalf("Open(%q) should have been rejected", bad)
		}
	}
}
