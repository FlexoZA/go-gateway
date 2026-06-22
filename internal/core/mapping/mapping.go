// Package mapping defines the neutral representation of a unit's editable
// code→event-code lookup tables, shared between protocol plugins (which provide
// the built-in defaults) and the postgres store (which persists and serves the
// front-end-editable versions).
package mapping

// Entry is one row of a unit's mapping table: within a named map_type, a raw
// device code maps to an ACM Standard Event Code.
type Entry struct {
	MapType     string
	Code        int
	EventCode   string
	Description string
}

// Table is a loaded set of mappings: map_type -> code -> event_code.
type Table map[string]map[int]string
