package postgres

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestDeleteOlderThan is a live-DB test (skipped without DATABASE_URL) for the
// media-retention reaper's delete-by-age queries: only rows created before the
// cutoff are removed, and their storage paths are returned so the caller can unlink
// the files.
func TestDeleteOlderThan(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping live-DB integration test")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.pool.Exec(ctx, `TRUNCATE clips, snapshots`); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().AddDate(0, 0, -30)

	// One old clip (created 40 days ago) and one recent clip.
	oldClip, err := s.CreateClip(ctx, "SER", 0, 0, 1, 2, "SER/0/old.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clips SET created_at = now() - interval '40 days' WHERE id = $1`, oldClip); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateClip(ctx, "SER", 0, 0, 1, 2, "SER/0/new.mp4"); err != nil {
		t.Fatal(err)
	}

	paths, err := s.DeleteClipsOlderThan(ctx, cutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "SER/0/old.mp4" {
		t.Fatalf("deleted clip paths = %v, want [SER/0/old.mp4]", paths)
	}
	if n, _ := s.CountClipsForTest(ctx); n != 1 {
		t.Fatalf("remaining clips = %d, want 1 (the recent one)", n)
	}

	// Same for snapshots.
	oldSnap, err := s.CreateSnapshot(ctx, "SER", 0, "general", "capture", 0, "", "snapshots/SER/old.jpg", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE snapshots SET created_at = now() - interval '40 days' WHERE id = $1`, oldSnap); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSnapshot(ctx, "SER", 0, "general", "capture", 0, "", "snapshots/SER/new.jpg", 10); err != nil {
		t.Fatal(err)
	}
	snapPaths, err := s.DeleteSnapshotsOlderThan(ctx, cutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapPaths) != 1 || snapPaths[0] != "snapshots/SER/old.jpg" {
		t.Fatalf("deleted snapshot paths = %v, want [snapshots/SER/old.jpg]", snapPaths)
	}
}

// CountClipsForTest counts clip rows (test helper; kept here so the test stays in
// the package without exporting production surface elsewhere).
func (s *Store) CountClipsForTest(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM clips`).Scan(&n)
	return n, err
}
