package howen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// config.go implements parameter config GET/SET (H-Protocol 0x40A0 → 0x10A0):
// reading and writing the unit's own settings (Wi-Fi, mobile, server, …).
//
// Firmware quirks (see dfm-mvr-gateway/docs/Howen/HOWEN_API.md §3):
//   - the 0x10A0 response does NOT echo our ss, so it can't be correlated by ss
//     like clips/file-query — we keep a single in-flight collector per session;
//   - segment names are case-normalised by the device → match case-insensitively;
//   - unknown segments are silently omitted from the reply;
//   - SET must send only the fields being changed (read-modify-write writes back
//     firmware garbage in some string fields).

// configCollector accumulates the (possibly multi-frame) 0x10A0 response.
type configCollector struct {
	mu     sync.Mutex
	want   map[string]bool // lower-cased segment names we asked for
	sc     map[string]any  // merged response segments (original-cased keys)
	err    error
	done   chan struct{}
	once   sync.Once
	expect bool          // true = GET (wait for wanted segments); false = SET (any reply ends it)
	grace  time.Duration // debounce window for trailing frames (GET)
	timer  *time.Timer   // fires finish() once frames stop arriving (segments the device lacks never come)
}

func (c *configCollector) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		if err != nil {
			c.err = err
		}
		if c.timer != nil {
			c.timer.Stop()
		}
		c.mu.Unlock()
		close(c.done)
	})
}

// collectParamConfig routes a 0x10A0 frame to the in-flight config op.
func (s *session) collectParamConfig(obj map[string]any) {
	s.configMu.Lock()
	c := s.configPending
	s.configMu.Unlock()
	if c == nil {
		return
	}
	if code := strings.TrimSpace(toString(obj["err"])); code != "" && code != "0" {
		c.finish(fmt.Errorf("device rejected config: err=%s", describeHowenError(code)))
		return
	}

	sc, _ := obj["sc"].(map[string]any)
	c.mu.Lock()
	for k, v := range sc {
		c.sc[k] = v
		delete(c.want, strings.ToLower(k))
	}
	remaining := len(c.want)
	// SET: any non-error reply ends it. GET: finish once all requested segments
	// arrived, else debounce — the device silently omits segments it doesn't have,
	// so complete with whatever we got once frames stop arriving.
	done := !c.expect || remaining == 0
	if !done {
		if c.timer != nil {
			c.timer.Stop()
		}
		c.timer = time.AfterFunc(c.grace, func() { c.finish(nil) })
	}
	c.mu.Unlock()
	if done {
		c.finish(nil)
	}
}

// runConfig sends a 0x40A0 frame and waits for the collected 0x10A0 reply.
func (s *session) runConfig(ctx context.Context, sc map[string]any, want []string, expect bool) (map[string]any, error) {
	if s.gate != gateApproved {
		return nil, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return nil, gateway.ErrDeviceSleeping
	}

	c := &configCollector{
		want:   map[string]bool{},
		sc:     map[string]any{},
		done:   make(chan struct{}),
		expect: expect,
		grace:  1500 * time.Millisecond,
	}
	for _, m := range want {
		c.want[strings.ToLower(m)] = true
	}

	// Only one config op in flight per session (responses aren't ss-correlated).
	s.configMu.Lock()
	if s.configPending != nil {
		s.configMu.Unlock()
		return nil, errors.New("another config request is in progress")
	}
	s.configPending = c
	s.configMu.Unlock()
	defer func() {
		s.configMu.Lock()
		s.configPending = nil
		s.configMu.Unlock()
	}()

	ss := fmt.Sprintf("cfg_%s_%s", s.serial, hexNow())
	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgParamConfig, map[string]any{"ss": ss, "sc": sc})); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, gateway.ErrCommandTimeout
	case <-c.done:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return c.sc, nil
}

// RequestConfig reads the named config segments (e.g. WIFI, DIALUP, SERVER).
func (s *session) RequestConfig(ctx context.Context, modules []string) (map[string]any, error) {
	clean := make([]string, 0, len(modules))
	sc := map[string]any{}
	for _, m := range modules {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		clean = append(clean, m)
		sc[m] = "" // empty value = read
	}
	if len(clean) == 0 {
		return nil, errors.New("no config modules requested")
	}
	return s.runConfig(ctx, sc, clean, true)
}

// UpdateConfig writes the given segments. Each segment must contain ONLY the
// fields being changed.
func (s *session) UpdateConfig(ctx context.Context, sc map[string]any) error {
	if len(sc) == 0 {
		return errors.New("no config changes provided")
	}
	for k, v := range sc {
		seg, ok := v.(map[string]any)
		if !ok || len(seg) == 0 {
			return fmt.Errorf("config segment %q must be a non-empty object", k)
		}
	}
	_, err := s.runConfig(ctx, sc, nil, false)
	return err
}
