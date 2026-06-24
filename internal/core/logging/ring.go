package logging

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ringCapacity is how many recent log entries the in-memory live-log buffer
// retains. ~2000 covers several minutes of busy activity; older entries roll off.
const ringCapacity = 2000

// levelRank orders levels for capture/threshold comparisons.
func levelRank(level string) int {
	switch level {
	case "debug":
		return 0
	case "info":
		return 1
	case "error":
		return 2
	default:
		return 1
	}
}

// Entry is one captured log line, surfaced by the live-log API.
type Entry struct {
	Seq    uint64         `json:"seq"`
	Time   string         `json:"time"`
	Level  string         `json:"level"`
	NS     string         `json:"ns"`
	Fields map[string]any `json:"fields"`
}

// ring is a fixed-size circular buffer of recent log entries, shared by reference
// across all With() children of a root logger. Its capture level (settable live)
// controls which levels are retained — so an operator can flip it to "debug" from
// the admin panel and watch per-frame device activity without restarting, while
// stdout verbosity stays governed by the DEBUG env var.
type ring struct {
	mu    sync.Mutex
	buf   []Entry
	start int // index of the oldest entry
	count int
	seq   uint64       // monotonic, never reset
	level atomic.Int32 // capture threshold rank (default info)
	now   func() time.Time
}

func newRing() *ring {
	r := &ring{buf: make([]Entry, ringCapacity), now: time.Now}
	r.level.Store(int32(levelRank("info")))
	return r
}

// wants reports whether an entry of the given level should be captured.
func (r *ring) wants(level string) bool {
	return levelRank(level) >= int(r.level.Load())
}

// push appends an entry, dropping the oldest when full. fields is copied so later
// mutation by the caller cannot corrupt the buffer.
func (r *ring) push(level, ns string, fields map[string]any) {
	if !r.wants(level) {
		return
	}
	cp := make(map[string]any, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	r.mu.Lock()
	r.seq++
	e := Entry{Seq: r.seq, Time: r.now().UTC().Format("2006-01-02T15:04:05.000Z07:00"), Level: level, NS: ns, Fields: cp}
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = e
		r.count++
	} else {
		r.buf[r.start] = e
		r.start = (r.start + 1) % len(r.buf)
	}
	r.mu.Unlock()
}

// since returns entries with Seq > after that match the filters, oldest-first,
// capped to the most recent `limit`. The returned cursor is the latest seq in the
// buffer (so a caller advances even when the newest entries are filtered out).
func (r *ring) since(after uint64, minLevel, unit, q string, limit int) ([]Entry, uint64) {
	minRank := levelRank(minLevel)
	unit = strings.ToLower(strings.TrimSpace(unit))
	q = strings.ToLower(strings.TrimSpace(q))

	r.mu.Lock()
	cursor := r.seq
	out := make([]Entry, 0, r.count)
	for i := 0; i < r.count; i++ {
		e := r.buf[(r.start+i)%len(r.buf)]
		if e.Seq <= after {
			continue
		}
		if levelRank(e.Level) < minRank {
			continue
		}
		if unit != "" && !strings.Contains(strings.ToLower(e.NS), unit) {
			continue
		}
		if q != "" && !entryMatches(e, q) {
			continue
		}
		out = append(out, e)
	}
	r.mu.Unlock()

	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, cursor
}

// entryMatches reports whether the lowercased query appears in the namespace or
// any of the entry's field keys/values.
func entryMatches(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.NS), q) {
		return true
	}
	for k, v := range e.Fields {
		if strings.Contains(strings.ToLower(k), q) {
			return true
		}
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}
