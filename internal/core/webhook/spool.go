package webhook

import (
	"context"
	"time"
)

// A Spool is a durable, crash-safe queue of pending webhook deliveries. It is what
// makes telemetry survive a webhook outage or a gateway restart: Consume records
// each message here instead of posting inline, and a background delivery pool
// (see Sink.Start) drains it with retry/backoff. The Postgres store implements it;
// the app adapts *postgres.Store to this interface.
//
// Delivery is at-least-once. A claimed item is leased (its next attempt is pushed
// out) so a crash mid-POST re-delivers it later rather than losing it; the trade-off
// is that a duplicate can reach the webhook if the process dies after the POST
// succeeds but before the item is deleted.
type Spool interface {
	// Enqueue durably records one pending delivery of body to each target. A message
	// with two enabled webhooks becomes two rows, retried independently.
	Enqueue(ctx context.Context, targets []string, body []byte) error

	// ClaimDue returns up to limit deliveries whose next attempt is due, atomically
	// leasing each (bumping its next attempt out by lease and its attempt count) so
	// concurrent workers never claim the same row. Delivered items must be removed
	// with Delete; failures rescheduled with Fail.
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]Delivery, error)

	// Delete removes a successfully delivered item.
	Delete(ctx context.Context, id int64) error

	// Fail reschedules a failed item for its next attempt and records the error.
	Fail(ctx context.Context, id int64, nextAttempt time.Time, lastErr string) error

	// Trim enforces the backlog cap, dropping the OLDEST deliveries beyond max, and
	// returns how many were dropped. A no-op (returns 0) when max <= 0.
	Trim(ctx context.Context, max int) (int64, error)

	// Pending reports the current backlog size, for the /api/metrics gauge.
	Pending(ctx context.Context) (int64, error)
}

// Delivery is one pending webhook POST held in the spool.
type Delivery struct {
	ID       int64
	Target   string
	Body     []byte
	Attempts int // delivery attempts already made (this claim included)
}

// backoff is the delay before a failed delivery's next attempt: exponential in the
// attempt count, capped, so a persistently-down webhook is retried at a steady low
// rate rather than hammered. attempts is 1-based (1 after the first failure).
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	// Cap the shift so 1<<n cannot overflow before the maxBackoff clamp.
	shift := attempts - 1
	if shift > 20 {
		shift = 20
	}
	d := baseBackoff << shift
	if d <= 0 || d > maxBackoff {
		return maxBackoff
	}
	return d
}
