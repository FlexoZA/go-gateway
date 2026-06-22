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
}

// New returns a logger bound to a namespace (e.g. "tcp/howen").
func New(namespace string) *Logger {
	l := &Logger{namespace: namespace, debugNS: map[string]bool{}, errSink: &atomic.Pointer[ErrorSink]{}}
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
	return &Logger{namespace: namespace, debugAll: l.debugAll, debugNS: l.debugNS, errSink: l.errSink}
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

// Debug emits only when debugging is enabled for this namespace.
func (l *Logger) Debug(fields map[string]any) {
	if l.enabled() {
		l.emit("debug", fields)
	}
}

func (l *Logger) emit(level string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
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
