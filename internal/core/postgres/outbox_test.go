package postgres

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestOutboxRoundTrip is a live-DB test (skipped without DATABASE_URL) covering the
// durable webhook queue: enqueue → claim (leases + increments) → fail (reschedules)
// → claim again → delete, plus backlog trimming.
func TestOutboxRoundTrip(t *testing.T) {
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
	// Start clean so counts are deterministic regardless of prior runs.
	if _, err := s.pool.Exec(ctx, `TRUNCATE webhook_outbox`); err != nil {
		t.Fatal(err)
	}

	// Enqueue one message for two targets -> two independent rows.
	if err := s.EnqueueOutbox(ctx, []string{"http://a", "http://b"}, []byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountOutbox(ctx); n != 2 {
		t.Fatalf("count=%d, want 2", n)
	}

	// Claim both (leased 60s ahead) — a second immediate claim sees nothing due.
	claimed, err := s.ClaimOutboxDue(ctx, 10, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed=%d, want 2", len(claimed))
	}
	if again, _ := s.ClaimOutboxDue(ctx, 10, 60*time.Second); len(again) != 0 {
		t.Fatalf("second claim got %d, want 0 (leased)", len(again))
	}
	for _, it := range claimed {
		if it.Attempts != 1 {
			t.Fatalf("attempts=%d, want 1", it.Attempts)
		}
	}

	// Deliver one (delete), fail the other back to due-now, then re-claim only it.
	if err := s.DeleteOutbox(ctx, claimed[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FailOutbox(ctx, claimed[1].ID, time.Now().Add(-time.Second), "boom"); err != nil {
		t.Fatal(err)
	}
	redue, err := s.ClaimOutboxDue(ctx, 10, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(redue) != 1 || redue[0].ID != claimed[1].ID {
		t.Fatalf("re-claim = %+v, want just the failed row", redue)
	}
	if redue[0].Attempts != 2 {
		t.Fatalf("attempts=%d after re-claim, want 2", redue[0].Attempts)
	}
	if err := s.DeleteOutbox(ctx, redue[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountOutbox(ctx); n != 0 {
		t.Fatalf("count=%d after draining, want 0", n)
	}

	// Trim keeps the newest max, dropping the oldest overflow.
	for i := 0; i < 5; i++ {
		if err := s.EnqueueOutbox(ctx, []string{"http://c"}, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	dropped, err := s.TrimOutbox(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 3 {
		t.Fatalf("dropped=%d, want 3", dropped)
	}
	if n, _ := s.CountOutbox(ctx); n != 2 {
		t.Fatalf("count=%d after trim, want 2", n)
	}
}
