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

// TestPruneEventMappingsLive verifies the prune DELETE (… code = ANY($3) with a
// Go []int) against a real database: a bypassed code is removed, an honored one
// is kept. Skipped without DATABASE_URL.
func TestPruneEventMappingsLive(t *testing.T) {
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

	const unit = "prunetest"
	if _, err := s.pool.Exec(ctx, `DELETE FROM event_mappings WHERE unit=$1`, unit); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM event_mappings WHERE unit=$1`, unit) })

	for _, r := range []struct {
		code int
		ev   string
	}{{41, "TRIP:START"}, {19, "IGNITION:OFF"}} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO event_mappings(unit,model,map_type,code,event_code) VALUES($1,'','event_code',$2,$3)`,
			unit, r.code, r.ev); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.PruneEventMappings(ctx, unit, "event_code", []int{41})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}

	var c41, c19 int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM event_mappings WHERE unit=$1 AND code=41`, unit).Scan(&c41)
	s.pool.QueryRow(ctx, `SELECT count(*) FROM event_mappings WHERE unit=$1 AND code=19`, unit).Scan(&c19)
	if c41 != 0 || c19 != 1 {
		t.Fatalf("after prune: bypassed code 41 count=%d (want 0), honored code 19 count=%d (want 1)", c41, c19)
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
