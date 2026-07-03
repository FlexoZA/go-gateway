package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// outbox.go backs the durable webhook delivery queue (webhook_outbox). This is the
// one place the gateway DB holds telemetry, and it does so only transiently: a row
// is a pending POST to an external telemetry endpoint, deleted the moment it is
// delivered. It is a delivery buffer, not a telemetry store of record — it exists so
// a webhook outage or a gateway restart does not lose GPS/event data in flight.
//
// These methods back the webhook.Spool interface (the app adapts *Store to it).

// OutboxItem is one pending webhook delivery claimed for a POST.
type OutboxItem struct {
	ID       int64
	Target   string
	Body     []byte
	Attempts int
}

// EnqueueOutbox records one pending delivery of body to each target (one row per
// target, retried independently). Due immediately (next_attempt_at defaults to now).
func (s *Store) EnqueueOutbox(ctx context.Context, targets []string, body []byte) error {
	if len(targets) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, t := range targets {
		b.Queue(`INSERT INTO webhook_outbox (target, body) VALUES ($1, $2)`, t, body)
	}
	br := s.pool.SendBatch(ctx, b)
	defer br.Close()
	for range targets {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("enqueue outbox: %w", err)
		}
	}
	return nil
}

// ClaimOutboxDue atomically leases up to limit due deliveries: it bumps each row's
// attempt count and pushes its next attempt out by lease (so concurrent workers and
// a crash-restart never double-process the same row within the lease window) and
// returns them. FOR UPDATE SKIP LOCKED lets multiple workers claim disjoint rows.
func (s *Store) ClaimOutboxDue(ctx context.Context, limit int, lease time.Duration) ([]OutboxItem, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE webhook_outbox
		    SET attempts = attempts + 1,
		        next_attempt_at = now() + make_interval(secs => $2)
		  WHERE id IN (
		        SELECT id FROM webhook_outbox
		         WHERE next_attempt_at <= now()
		         ORDER BY id
		         LIMIT $1
		         FOR UPDATE SKIP LOCKED
		  )
		  RETURNING id, target, body, attempts`,
		limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim outbox: %w", err)
	}
	defer rows.Close()

	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		if err := rows.Scan(&it.ID, &it.Target, &it.Body, &it.Attempts); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteOutbox removes a delivered item.
func (s *Store) DeleteOutbox(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM webhook_outbox WHERE id = $1`, id)
	return err
}

// FailOutbox reschedules a failed item to nextAttempt and records the error.
func (s *Store) FailOutbox(ctx context.Context, id int64, nextAttempt time.Time, lastErr string) error {
	if len(lastErr) > 500 {
		lastErr = lastErr[:500]
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_outbox SET next_attempt_at = $2, last_error = $3 WHERE id = $1`,
		id, nextAttempt, lastErr)
	return err
}

// TrimOutbox enforces the backlog cap, deleting the OLDEST deliveries beyond the
// newest max, and returns how many were dropped. Bounds memory/disk during a long
// outage at the cost of losing the oldest (least-current) telemetry first. No-op
// when max <= 0.
func (s *Store) TrimOutbox(ctx context.Context, max int) (int64, error) {
	if max <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webhook_outbox WHERE id IN (
		    SELECT id FROM webhook_outbox ORDER BY id DESC OFFSET $1
		 )`, max)
	if err != nil {
		return 0, fmt.Errorf("trim outbox: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CountOutbox returns the current backlog size.
func (s *Store) CountOutbox(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_outbox`).Scan(&n)
	return n, err
}
