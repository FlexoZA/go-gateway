package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dfm/device-gateway/internal/core/eventcodes"
)

// eventcodes.go backs the standard_event_codes picklist served to the admin
// panel. The list is seeded from the embedded CSV on startup.

// SeedStandardEventCodes upserts the canonical event codes. Existing rows are
// refreshed from the CSV (it is the source of truth); custom rows absent from
// the CSV are left untouched.
func (s *Store) SeedStandardEventCodes(ctx context.Context, codes []eventcodes.Code) error {
	if len(codes) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, c := range codes {
		batch.Queue(
			`INSERT INTO standard_event_codes (code, category, notes, device_notes)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (code) DO UPDATE
			   SET category = EXCLUDED.category, notes = EXCLUDED.notes,
			       device_notes = EXCLUDED.device_notes, updated_at = now()`,
			c.Code, nullIfEmpty(c.Category), nullIfEmpty(c.Notes), nullIfEmpty(c.DeviceNotes),
		)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range codes {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("seed standard event codes: %w", err)
		}
	}
	return nil
}

// ListStandardEventCodes returns the picklist, ordered by category then code.
func (s *Store) ListStandardEventCodes(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT code, COALESCE(category, ''), COALESCE(notes, ''), COALESCE(device_notes, '')
		 FROM standard_event_codes ORDER BY category, code`)
	if err != nil {
		return nil, fmt.Errorf("list standard event codes: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var code, category, notes, deviceNotes string
		if err := rows.Scan(&code, &category, &notes, &deviceNotes); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"code":         code,
			"category":     category,
			"notes":        notes,
			"device_notes": deviceNotes,
		})
	}
	return out, rows.Err()
}
