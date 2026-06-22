// Package eventcodes provides the canonical list of ACM Standard Event Codes.
//
// The list is the single source of truth that the front end offers when picking
// an event code for a mapping. It is embedded from the official CSV export
// (docs/gateway/Standard Event Codes - Events.csv in the original gateway) so the
// gateway is self-contained, and seeded into the standard_event_codes table on
// startup so the API can serve it.
package eventcodes

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"io"
	"strings"
)

//go:embed standard_event_codes.csv
var csvData []byte

// Code is one standard event code with its category and notes.
type Code struct {
	Code        string `json:"code"`
	Category    string `json:"category"`
	Notes       string `json:"notes"`
	DeviceNotes string `json:"device_notes"`
}

// Standard parses and returns the embedded standard event codes. The CSV groups
// rows by category, leaving the category cell blank on continuation rows, so the
// category is forward-filled. Leading metadata rows and the header are skipped,
// as are rows with no event code.
func Standard() []Code {
	r := csv.NewReader(bytes.NewReader(csvData))
	r.FieldsPerRecord = -1 // metadata rows have a different column count

	var out []Code
	var category string
	headerSeen := false
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(rec) < 2 {
			continue
		}
		col0 := strings.TrimSpace(rec[0])
		col1 := strings.TrimSpace(rec[1])

		if !headerSeen {
			if col0 == "Event Category" {
				headerSeen = true
			}
			continue
		}
		if col0 != "" {
			category = col0
		}
		if col1 == "" {
			continue
		}
		c := Code{Code: col1, Category: category}
		if len(rec) > 2 {
			c.Notes = strings.TrimSpace(rec[2])
		}
		if len(rec) > 3 {
			c.DeviceNotes = strings.TrimSpace(rec[3])
		}
		out = append(out, c)
	}
	return out
}
