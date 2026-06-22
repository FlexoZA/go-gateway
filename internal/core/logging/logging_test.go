package logging

import (
	"sync"
	"testing"
)

// recordingSink is a concurrency-safe ErrorSink that captures what it receives.
type recordingSink struct {
	mu   sync.Mutex
	hits []string // namespace of each call
}

func (r *recordingSink) fn(namespace string, _ map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits = append(r.hits, namespace)
}

func (r *recordingSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.hits)
}

func TestErrorSinkOnlyFiresOnError(t *testing.T) {
	var sink recordingSink
	l := New("root")
	l.SetErrorSink(sink.fn)

	l.Info(map[string]any{"event": "info"})
	l.Debug(map[string]any{"event": "debug"})
	if got := sink.count(); got != 0 {
		t.Fatalf("Info/Debug should not reach the error sink; got %d hits", got)
	}

	l.Error(map[string]any{"event": "boom"})
	if got := sink.count(); got != 1 {
		t.Fatalf("Error should reach the sink exactly once; got %d", got)
	}
}

func TestErrorSinkSharedAcrossWithChildren(t *testing.T) {
	var sink recordingSink
	root := New("root")

	// Child derived BEFORE the sink is installed must still see it, because the
	// sink is shared by reference (this mirrors how the gateway derives loggers at
	// startup but installs the sink only after the DB connects).
	child := root.With("tcp/howen")
	root.SetErrorSink(sink.fn)

	child.Error(map[string]any{"event": "boom"})
	if got := sink.count(); got != 1 {
		t.Fatalf("child logger should share the root's sink; got %d hits", got)
	}
	if sink.hits[0] != "tcp/howen" {
		t.Fatalf("sink should receive the emitting logger's namespace; got %q", sink.hits[0])
	}
}

func TestErrorSinkClearable(t *testing.T) {
	var sink recordingSink
	l := New("root")
	l.SetErrorSink(sink.fn)
	l.SetErrorSink(nil)

	l.Error(map[string]any{"event": "boom"})
	if got := sink.count(); got != 0 {
		t.Fatalf("cleared sink should receive nothing; got %d hits", got)
	}
}
