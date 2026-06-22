// Package webhook delivers a built universal message to one or more HTTP sinks —
// the external database/N8N endpoints that store all GPS/event data. The target
// list is runtime-swappable (it is managed from the admin panel's server
// settings) so the sink reads the current set on every delivery. An empty list
// makes it a no-op.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dfm/device-gateway/internal/core/message"
)

// Sink POSTs the universal message as JSON to every currently-enabled URL.
type Sink struct {
	targets atomic.Pointer[[]string]
	HTTP    *http.Client
}

// New constructs a webhook sink with an optional initial set of target URLs.
func New(urls ...string) *Sink {
	s := &Sink{HTTP: &http.Client{Timeout: 15 * time.Second}}
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

// Consume delivers the built message to every target. The body is marshalled
// once and POSTed to all targets concurrently; a failure to one does not stop
// the others, and all errors are joined. No-op when there are no targets.
func (s *Sink) Consume(ctx context.Context, _ message.Inbound, msg message.Universal) error {
	targets := s.Targets()
	if len(targets) == 0 {
		return nil
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(targets) == 1 {
		return s.post(ctx, targets[0], body)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, url := range targets {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			errs[i] = s.post(ctx, url, body)
		}(i, url)
	}
	wg.Wait()
	return errors.Join(errs...)
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
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s responded %d", url, resp.StatusCode)
	}
	return nil
}
