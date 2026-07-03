package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/message"
)

// memSpool is an in-memory Spool for exercising the delivery worker without a DB.
// It honours the same lease/backoff contract as the Postgres implementation.
type memSpool struct {
	mu     sync.Mutex
	items  []*memItem
	nextID int64
}

type memItem struct {
	id       int64
	target   string
	body     []byte
	attempts int
	next     time.Time
}

func (m *memSpool) Enqueue(_ context.Context, targets []string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range targets {
		m.nextID++
		m.items = append(m.items, &memItem{id: m.nextID, target: t, body: body})
	}
	return nil
}

func (m *memSpool) ClaimDue(_ context.Context, limit int, lease time.Duration) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []Delivery
	for _, it := range m.items {
		if len(out) >= limit {
			break
		}
		if it.next.After(now) {
			continue
		}
		it.attempts++
		it.next = now.Add(lease)
		out = append(out, Delivery{ID: it.id, Target: it.target, Body: it.body, Attempts: it.attempts})
	}
	return out, nil
}

func (m *memSpool) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, it := range m.items {
		if it.id == id {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *memSpool) Fail(_ context.Context, id int64, next time.Time, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.id == id {
			it.next = next
		}
	}
	return nil
}

func (m *memSpool) Trim(_ context.Context, max int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if max <= 0 || len(m.items) <= max {
		return 0, nil
	}
	drop := len(m.items) - max
	m.items = m.items[drop:] // keep newest max, drop oldest
	return int64(drop), nil
}

func (m *memSpool) Pending(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.items)), nil
}

// makeAllDue simulates the passage of the backoff window so a test need not sleep
// through real retry delays.
func (m *memSpool) makeAllDue() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		it.next = time.Time{}
	}
}

func eventually(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within " + d.String())
}

// TestDurableDeliveryDrains: with a spool, Consume enqueues and the worker pool
// delivers to a healthy endpoint, draining the backlog to zero.
func TestDurableDeliveryDrains(t *testing.T) {
	var got int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&got, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	mem := &memSpool{}
	s := NewWithSpool(mem, nil, 0, ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	for i := 0; i < 3; i++ {
		if err := s.Consume(ctx, message.Inbound{}, message.Universal{}); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	eventually(t, 5*time.Second, func() bool {
		n, _ := mem.Pending(ctx)
		return n == 0 && atomic.LoadInt64(&got) == 3
	})
	if s.stats.delivered.Load() != 3 {
		t.Fatalf("delivered=%d, want 3", s.stats.delivered.Load())
	}
}

// TestDurableDeliveryRetriesThenSucceeds: a delivery that fails is kept, rescheduled
// (attempts incremented, not dropped), and delivered once the endpoint recovers —
// i.e. an outage loses nothing.
func TestDurableDeliveryRetriesThenSucceeds(t *testing.T) {
	var healthy atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer ts.Close()

	mem := &memSpool{}
	s := NewWithSpool(mem, nil, 0, ts.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	if err := s.Consume(ctx, message.Inbound{}, message.Universal{}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// Endpoint is down: the item must be retried (attempts climb) but never lost.
	eventually(t, 5*time.Second, func() bool { return s.stats.failed.Load() >= 1 })
	if n, _ := mem.Pending(ctx); n != 1 {
		t.Fatalf("backlog=%d during outage, want 1 (nothing lost)", n)
	}

	// Endpoint recovers; simulate the backoff window elapsing.
	healthy.Store(true)
	mem.makeAllDue()
	eventually(t, 5*time.Second, func() bool {
		n, _ := mem.Pending(ctx)
		return n == 0 && s.stats.delivered.Load() == 1
	})
}

// TestDirectModeRetries: without a spool, Consume posts directly and retries a
// transient failure before succeeding; a permanent failure returns an error.
func TestDirectModeRetries(t *testing.T) {
	var attempts int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt64(&attempts, 1) < 2 {
			w.WriteHeader(500) // first attempt fails, second succeeds
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	s := New(ts.URL)
	if err := s.Consume(context.Background(), message.Inbound{}, message.Universal{}); err != nil {
		t.Fatalf("direct consume should have succeeded on retry: %v", err)
	}
	if s.stats.delivered.Load() != 1 {
		t.Fatalf("delivered=%d, want 1", s.stats.delivered.Load())
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer down.Close()
	s2 := New(down.URL)
	if err := s2.Consume(context.Background(), message.Inbound{}, message.Universal{}); err == nil {
		t.Fatal("direct consume against a permanently-failing endpoint should error")
	}
	if s2.stats.failed.Load() != directAttempts {
		t.Fatalf("failed=%d, want %d", s2.stats.failed.Load(), directAttempts)
	}
}

// TestBackoffCapped: the retry delay grows with attempts but never exceeds maxBackoff
// and never underflows to a non-positive duration.
func TestBackoffCapped(t *testing.T) {
	for _, attempts := range []int{0, 1, 2, 5, 20, 100, 1000} {
		d := backoff(attempts)
		if d <= 0 {
			t.Fatalf("backoff(%d)=%v, must be positive", attempts, d)
		}
		if d > maxBackoff {
			t.Fatalf("backoff(%d)=%v, exceeds cap %v", attempts, d, maxBackoff)
		}
	}
	if backoff(1) != baseBackoff {
		t.Fatalf("backoff(1)=%v, want base %v", backoff(1), baseBackoff)
	}
}
