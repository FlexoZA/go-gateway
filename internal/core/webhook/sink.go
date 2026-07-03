// Package webhook delivers a built universal message to one or more HTTP sinks —
// the external database/N8N endpoints that store all GPS/event data. The target
// list is runtime-swappable (it is managed from the admin panel's server
// settings) so the sink reads the current set on every delivery. An empty list
// makes it a no-op.
//
// Because the webhook is the ONLY telemetry sink (the gateway DB stores no GPS/event
// data), delivery must not silently drop on an outage. With a durable Spool the sink
// enqueues every message and a background worker pool drains it with retry/backoff,
// so telemetry survives a webhook outage or a gateway restart. Without a spool (no
// database, e.g. a lean single-unit build) it posts directly with a bounded retry —
// best-effort, no cross-restart durability — and logs failures loudly.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/message"
)

const (
	// baseBackoff / maxBackoff bound the exponential retry schedule for a failed
	// delivery (see backoff). A down webhook is retried steadily, not hammered.
	baseBackoff = 5 * time.Second
	maxBackoff  = 5 * time.Minute

	// claimLease is how long a claimed-but-not-yet-resolved delivery is hidden from
	// other workers. It only matters on a crash mid-POST: the item re-delivers after
	// the lease rather than being lost or double-claimed by a live worker.
	claimLease = 60 * time.Second

	// deliverWorkers / claimBatch / pollInterval tune the drain loop.
	deliverWorkers = 4
	claimBatch     = 128
	pollInterval   = 1 * time.Second

	// trimInterval is how often the backlog cap is enforced.
	trimInterval = 30 * time.Second

	// problemLogEvery throttles delivery-failure Error logs so a sustained outage
	// does not flood gateway_errors — one summary line per interval, not per message.
	problemLogEvery = 30 * time.Second

	// directAttempts is how many times direct (no-spool) mode retries a POST before
	// giving up on that message. Kept small: it holds the emit goroutine.
	directAttempts = 3
)

// Sink POSTs the universal message as JSON to every currently-enabled URL.
type Sink struct {
	targets atomic.Pointer[[]string]
	HTTP    *http.Client
	log     *logging.Logger

	// spool, when set, makes delivery durable: Consume enqueues and Start's workers
	// drain. Nil selects direct best-effort delivery.
	spool      Spool
	maxBacklog int
	started    atomic.Bool

	lastProblemLog atomic.Int64 // unix seconds of the last delivery-failure Error log
	stats          stats
}

// stats are cheap counters surfaced on /api/metrics so a stuck telemetry path is
// visible instead of silent.
type stats struct {
	enqueued  atomic.Int64 // deliveries recorded into the spool
	delivered atomic.Int64 // deliveries that got a 2xx
	failed    atomic.Int64 // individual failed attempts (retried unless trimmed)
	dropped   atomic.Int64 // deliveries dropped by the backlog cap
}

// New constructs a direct-delivery webhook sink (best-effort, no durability) with an
// optional initial set of target URLs. Used when no database is configured.
func New(urls ...string) *Sink {
	s := &Sink{HTTP: &http.Client{Timeout: 15 * time.Second}, log: logging.New("webhook")}
	s.SetTargets(urls)
	return s
}

// NewWithSpool constructs a durable webhook sink backed by spool. Call Start to run
// the delivery workers. maxBacklog caps the spool (0 = unbounded).
func NewWithSpool(spool Spool, log *logging.Logger, maxBacklog int, urls ...string) *Sink {
	if log == nil {
		log = logging.New("webhook")
	}
	s := &Sink{
		HTTP:       &http.Client{Timeout: 15 * time.Second},
		log:        log.With("webhook"),
		spool:      spool,
		maxBacklog: maxBacklog,
	}
	s.SetTargets(urls)
	return s
}

// SetTargets atomically replaces the delivery targets. Empty/blank URLs are
// dropped and duplicates collapsed. Safe to call concurrently with Consume — the
// next delivery uses the new set.
func (s *Sink) SetTargets(urls []string) {
	seen := map[string]bool{}
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		cleaned = append(cleaned, u)
	}
	s.targets.Store(&cleaned)
}

// Targets returns the current delivery targets.
func (s *Sink) Targets() []string {
	if p := s.targets.Load(); p != nil {
		return *p
	}
	return nil
}

// Enabled reports whether at least one target is configured.
func (s *Sink) Enabled() bool { return len(s.Targets()) > 0 }

// Consume records the built message for delivery to every target. With a spool it
// enqueues durably (fast) and returns; the worker pool delivers. Without one it
// posts directly with a bounded retry. No-op when there are no targets.
func (s *Sink) Consume(ctx context.Context, _ message.Inbound, msg message.Universal) error {
	targets := s.Targets()
	if len(targets) == 0 {
		return nil
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if s.spool != nil {
		if err := s.spool.Enqueue(ctx, targets, body); err != nil {
			// The read loop's emit goroutine sees this; make it loud — a failing
			// enqueue means telemetry is being lost at the source.
			s.log.Error(map[string]any{"event": "webhook_enqueue_failed", "targets": len(targets), "error": err.Error()})
			return err
		}
		s.stats.enqueued.Add(int64(len(targets)))
		return nil
	}
	return s.deliverDirect(ctx, targets, body)
}

// deliverDirect posts to every target with a bounded retry (no-spool mode). Errors
// are joined and logged at Error on final give-up.
func (s *Sink) deliverDirect(ctx context.Context, targets []string, body []byte) error {
	var errs []error
	for _, url := range targets {
		var lastErr error
		for attempt := 1; attempt <= directAttempts; attempt++ {
			if lastErr = s.post(ctx, url, body); lastErr == nil {
				s.stats.delivered.Add(1)
				break
			}
			s.stats.failed.Add(1)
			if attempt < directAttempts {
				select {
				case <-ctx.Done():
					lastErr = ctx.Err()
				case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
					continue
				}
			}
			break
		}
		if lastErr != nil {
			s.log.Error(map[string]any{"event": "webhook_delivery_failed", "url": url, "error": lastErr.Error()})
			errs = append(errs, lastErr)
		}
	}
	return errors.Join(errs...)
}

// Start launches the durable delivery workers and the backlog trimmer. No-op in
// direct mode or if already started. They run until ctx is cancelled.
func (s *Sink) Start(ctx context.Context) {
	if s.spool == nil || !s.started.CompareAndSwap(false, true) {
		return
	}
	for i := 0; i < deliverWorkers; i++ {
		go s.deliverLoop(ctx)
	}
	go s.trimLoop(ctx)
	s.log.Info(map[string]any{"event": "webhook_delivery_started", "workers": deliverWorkers, "max_backlog": s.maxBacklog})
}

func (s *Sink) deliverLoop(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Drain due items until a batch comes back short, then wait for the next
			// tick — so a large backlog is worked off promptly, not one batch/second.
			for ctx.Err() == nil {
				n, err := s.deliverBatch(ctx)
				if err != nil {
					s.logProblem("webhook_claim_failed", "", err.Error())
					break
				}
				if n < claimBatch {
					break
				}
			}
		}
	}
}

// deliverBatch claims and delivers up to claimBatch due items; returns how many were
// claimed (0 when the queue is idle).
func (s *Sink) deliverBatch(ctx context.Context) (int, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	items, err := s.spool.ClaimDue(cctx, claimBatch, claimLease)
	cancel()
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if err := s.post(ctx, it.Target, it.Body); err != nil {
			s.stats.failed.Add(1)
			next := time.Now().Add(backoff(it.Attempts))
			fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = s.spool.Fail(fctx, it.ID, next, err.Error())
			cancel()
			s.logProblem("webhook_delivery_failed", it.Target, err.Error())
			continue
		}
		s.stats.delivered.Add(1)
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := s.spool.Delete(dctx, it.ID); err != nil {
			// Delivered but not removed: it will re-deliver after the lease (a
			// duplicate). Rare; log so it is not invisible.
			s.log.Error(map[string]any{"event": "webhook_delete_failed", "id": it.ID, "error": err.Error()})
		}
		cancel()
	}
	return len(items), nil
}

func (s *Sink) trimLoop(ctx context.Context) {
	if s.maxBacklog <= 0 {
		return
	}
	t := time.NewTicker(trimInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			n, err := s.spool.Trim(cctx, s.maxBacklog)
			cancel()
			if err != nil {
				s.logProblem("webhook_trim_failed", "", err.Error())
				continue
			}
			if n > 0 {
				s.stats.dropped.Add(n)
				s.log.Error(map[string]any{"event": "webhook_backlog_trimmed", "dropped": n, "cap": s.maxBacklog})
			}
		}
	}
}

// logProblem emits a delivery-failure Error at most once per problemLogEvery, so a
// sustained outage produces a steady heartbeat in gateway_errors, not a flood.
func (s *Sink) logProblem(event, target, msg string) {
	now := time.Now().Unix()
	last := s.lastProblemLog.Load()
	if now-last < int64(problemLogEvery/time.Second) {
		return
	}
	if !s.lastProblemLog.CompareAndSwap(last, now) {
		return
	}
	fields := map[string]any{"event": event, "error": msg, "failed_total": s.stats.failed.Load()}
	if target != "" {
		fields["url"] = target
	}
	if s.spool != nil {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if n, err := s.spool.Pending(cctx); err == nil {
			fields["backlog"] = n
		}
		cancel()
	}
	s.log.Error(fields)
}

// Stats returns delivery counters plus the current spool backlog, for /api/metrics.
func (s *Sink) Stats(ctx context.Context) map[string]any {
	out := map[string]any{
		"targets":   len(s.Targets()),
		"durable":   s.spool != nil,
		"enqueued":  s.stats.enqueued.Load(),
		"delivered": s.stats.delivered.Load(),
		"failed":    s.stats.failed.Load(),
		"dropped":   s.stats.dropped.Load(),
	}
	if s.spool != nil {
		if n, err := s.spool.Pending(ctx); err == nil {
			out["backlog"] = n
		}
	}
	return out
}

func (s *Sink) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("webhook %s: %w", url, err)
	}
	// Drain then close so the keep-alive connection can be reused instead of a new
	// TLS handshake per message.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s responded %d", url, resp.StatusCode)
	}
	return nil
}
