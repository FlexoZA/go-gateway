// Package mapping defines the neutral representation of a unit's editable
// code→event-code lookup tables, shared between protocol plugins (which provide
// the built-in defaults) and the postgres store (which persists and serves the
// front-end-editable versions).
package mapping

// Entry is one row of a unit's mapping table: within a named map_type, a raw
// device code maps to an ACM Standard Event Code. Model scopes the row to a
// specific device model; an empty Model is the unit-wide default that applies to
// any model without its own table.
type Entry struct {
	Model       string
	MapType     string
	Code        int
	EventCode   string
	Description string
}

// Table is a loaded set of mappings: map_type -> code -> event_code.
type Table map[string]map[int]string

// ByModel is a unit's mappings grouped by device model: model -> Table. The empty
// model key ("") is the unit-wide default.
type ByModel map[string]Table

// Prune names editable rows a unit no longer honors — codes it resolves with
// built-in logic and never reads from the editable table. The runner deletes any
// such rows older builds seeded, so the admin's editable set stays limited to
// rows that actually take effect.
type Prune struct {
	MapType string
	Codes   []int
}
