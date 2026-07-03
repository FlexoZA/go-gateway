// Package logging provides a tiny structured logger used across the gateway.
// Output is single-line JSON so container log scrapers can parse it directly.
package logging

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync/atomic"
)

// Emit pure JSON: clear the stdlib logger's default date/time prefix so each line
// is a single valid JSON object (the container runtime timestamps the line itself).
func init() { log.SetFlags(0) }

// ErrorSink receives the fields of every Error-level line, in addition to
// stdout. It lets the gateway persist its own errors (e.g. to Postgres) without
// the logging package importing the storage layer. It is invoked synchronously
// inside Error(), so an implementation MUST NOT block and MUST NOT call back
// into Error (use Debug if it needs to report its own failures) — otherwise it
// would recurse. The standard implementation hands off to a buffered channel.
type ErrorSink func(namespace string, fields map[string]any)

// Logger emits namespaced structured log lines. Debug lines are suppressed
// unless DEBUG=1 (or the namespace appears in DEBUG as a comma list).
type Logger struct {
	namespace string
	debugAll  bool
	debugNS   map[string]bool
	// errSink is shared by reference across all With() children of a root logger,
	// so SetErrorSink installed once (e.g. after the DB connects) takes effect for
	// loggers that were already derived. nil entry = no sink.
	errSink *atomic.Pointer[ErrorSink]
	// ring is the in-memory live-log buffer, shared by reference across all With()
	// children so every namespace's lines land in one stream the API can tail.
	ring *ring
}

// New returns a logger bound to a namespace (e.g. "tcp/howen").
func New(namespace string) *Logger {
	l := &Logger{namespace: namespace, debugNS: map[string]bool{}, errSink: &atomic.Pointer[ErrorSink]{}, ring: newRing()}
	raw := strings.TrimSpace(os.Getenv("DEBUG"))
	switch raw {
	case "":
		// debug disabled
	case "1", "true", "*", "all":
		l.debugAll = true
	default:
		for _, ns := range strings.Split(raw, ",") {
			if ns = strings.TrimSpace(ns); ns != "" {
				l.debugNS[ns] = true
			}
		}
	}
	return l
}

// With returns a child logger with a different namespace, inheriting debug config
// and sharing the parent's error sink (by reference, so a sink installed later on
// any related logger is seen here too).
func (l *Logger) With(namespace string) *Logger {
	return &Logger{namespace: namespace, debugAll: l.debugAll, debugNS: l.debugNS, errSink: l.errSink, ring: l.ring}
}

// SetErrorSink installs (or, with nil, clears) the sink that receives every
// Error-level line. It affects this logger and all its With() relatives. Safe to
// call concurrently with logging.
func (l *Logger) SetErrorSink(fn ErrorSink) {
	if fn == nil {
		l.errSink.Store(nil)
		return
	}
	l.errSink.Store(&fn)
}

func (l *Logger) enabled() bool {
	return l.debugAll || l.debugNS[l.namespace]
}

// Info always emits.
func (l *Logger) Info(fields map[string]any) { l.emit("info", fields) }

// Error always emits, and additionally hands the fields to the error sink (if
// one is installed) so the error can be persisted.
func (l *Logger) Error(fields map[string]any) {
	l.emit("error", fields)
	if p := l.errSink.Load(); p != nil {
		(*p)(l.namespace, fields)
	}
}

// Debug emits to stdout only when debugging is enabled for this namespace, but is
// still captured into the live-log ring when the ring's capture level allows it —
// so an operator can watch debug activity from the panel without a stdout restart.
func (l *Logger) Debug(fields map[string]any) {
	stdout := l.enabled()
	ring := l.ring != nil && l.ring.wants("debug")
	if !stdout && !ring {
		return
	}
	l.dispatch("debug", fields, stdout)
}

// SetCaptureLevel sets the live-log ring's capture threshold ("debug" | "info" |
// "error"); affects this logger and all its With() relatives (shared ring).
func (l *Logger) SetCaptureLevel(level string) {
	if l.ring != nil {
		l.ring.level.Store(int32(levelRank(level)))
	}
}

// CaptureLevel returns the ring's current capture threshold as a string.
func (l *Logger) CaptureLevel() string {
	if l.ring == nil {
		return "info"
	}
	switch int(l.ring.level.Load()) {
	case 0:
		return "debug"
	case 2:
		return "error"
	default:
		return "info"
	}
}

// LiveSince returns ring entries newer than `after` matching the filters (minLevel
// "debug"|"info"|"error", unit substring of the namespace, q free-text), plus the
// latest cursor. Used by the live-log API.
func (l *Logger) LiveSince(after uint64, minLevel, unit, q string, limit int) ([]Entry, uint64) {
	if l.ring == nil {
		return nil, 0
	}
	return l.ring.since(after, minLevel, unit, q, limit)
}

func (l *Logger) emit(level string, fields map[string]any) { l.dispatch(level, fields, true) }

// dispatch writes the line to stdout (when stdout is true) and always offers it to
// the live-log ring (which decides by capture level).
func (l *Logger) dispatch(level string, fields map[string]any, stdout bool) {
	if fields == nil {
		fields = map[string]any{}
	}
	if l.ring != nil {
		l.ring.push(level, l.namespace, fields)
	}
	if !stdout {
		return
	}
	out := map[string]any{"ns": l.namespace, "level": level}
	for k, v := range fields {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		log.Printf("%s %s %v", l.namespace, level, fields)
		return
	}
	log.Println(string(b))
}
