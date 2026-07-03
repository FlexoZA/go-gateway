package postgres

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// TestBackupRoundTrip is a live-DB test (skipped without DATABASE_URL): dump the
// gateway tables, mutate the DB, restore, and confirm the original rows come back
// and the id sequence is re-aligned so a new insert doesn't collide.
func TestBackupRoundTrip(t *testing.T) {
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
	if _, err := s.pool.Exec(ctx, `TRUNCATE devices, users, server_settings, webhooks`); err != nil {
		t.Fatal(err)
	}

	// Seed a representative slice of state across pk styles (TEXT pk, serial id).
	if _, err := s.pool.Exec(ctx, `INSERT INTO devices (serial, protocol, status) VALUES ('SER1','howen','online')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "gateway_name", "gw-original"); err != nil {
		t.Fatal(err)
	}
	whID, err := s.CreateWebhook(ctx, "primary", "http://sink", true)
	if err != nil {
		t.Fatal(err)
	}

	// Back it up.
	var buf bytes.Buffer
	if _, err := s.DumpTo(ctx, &buf, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Mutate: change the setting, add a stray device, delete the webhook.
	if err := s.SetSetting(ctx, "gateway_name", "gw-changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO devices (serial, protocol) VALUES ('STRAY','x')`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWebhook(ctx, whID); err != nil {
		t.Fatal(err)
	}

	// Restore and verify the snapshot state is back exactly.
	if err := s.RestoreFrom(ctx, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.GetSetting(ctx, "gateway_name"); v != "gw-original" {
		t.Fatalf("setting after restore = %q, want gw-original", v)
	}
	var devCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM devices`).Scan(&devCount); err != nil {
		t.Fatal(err)
	}
	if devCount != 1 {
		t.Fatalf("devices after restore = %d, want 1 (STRAY dropped)", devCount)
	}
	whs, err := s.ListWebhooks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(whs) != 1 {
		t.Fatalf("webhooks after restore = %d, want 1", len(whs))
	}

	// The webhook id sequence must be re-aligned: a fresh insert must not collide
	// with the restored row's id.
	if _, err := s.CreateWebhook(ctx, "second", "http://sink2", false); err != nil {
		t.Fatalf("insert after restore (sequence not reset?): %v", err)
	}
}
