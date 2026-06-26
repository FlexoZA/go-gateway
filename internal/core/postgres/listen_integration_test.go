package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestListenDoesNotStarvePool is a live-DB test (skipped without DATABASE_URL,
// so CI without a database is unaffected). It pins the pool to 2 connections and
// starts 3 listeners; before the fix each listener hijacked a pool connection,
// so 3 listeners would exhaust the pool and queries would block. It asserts the
// pool still serves queries with the listeners active, and that a NOTIFY still
// round-trips to a listener.
func TestListenDoesNotStarvePool(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping live-DB integration test")
	}
	if strings.Contains(dsn, "?") {
		dsn += "&pool_max_conns=2"
	} else {
		dsn += "?pool_max_conns=2"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := New(ctx, dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Start more long-lived listeners than the pool has connections.
	events := make(chan string, 32)
	go s.ListenForMappingChanges(ctx, func(u string) { events <- "map:" + u })
	go s.ListenForSettingsChanges(ctx, func(k string) { events <- "set:" + k })
	go s.ListenForWebhookChanges(ctx, func(string) { events <- "wh" })
	time.Sleep(500 * time.Millisecond) // let them connect + do the initial resync

	// The 2-connection pool must still serve queries despite 3 active listeners.
	for i := 0; i < 5; i++ {
		qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
		var one int
		err := s.pool.QueryRow(qctx, "SELECT 1").Scan(&one)
		qcancel()
		if err != nil {
			t.Fatalf("pool query %d failed (pool starved by listeners?): %v", i, err)
		}
	}

	// Drain the initial resync events, then prove a NOTIFY still arrives.
	drain(events)
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO server_settings(key,value) VALUES('starve_test','1')
		 ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if strings.HasPrefix(ev, "set:") {
				return // NOTIFY delivered through the dedicated listener connection
			}
		case <-deadline:
			t.Fatal("server_settings NOTIFY did not arrive at the listener")
		}
	}
}

func drain(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
