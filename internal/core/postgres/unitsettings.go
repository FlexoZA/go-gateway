package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// unitsettings.go backs the per-unit-type gateway settings (key/value, scoped by
// unit) — a unit's own editable settings declared by its SettingsSchema, distinct
// from the global server_settings and from per-device parameter config.

// SeedUnitSettingDefault inserts a unit setting only if it does not already exist,
// so a unit's schema default seeds the row once and later edits are never
// overwritten.
func (s *Store) SeedUnitSettingDefault(ctx context.Context, unit, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO unit_settings (unit, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (unit, key) DO NOTHING`, unit, key, value)
	return err
}

// SetUnitSetting upserts a unit setting value. Firing the NOTIFY makes the running
// gateway hot-reload that unit's settings within milliseconds.
func (s *Store) SetUnitSetting(ctx context.Context, unit, key, value string) error {
	unit = strings.TrimSpace(unit)
	key = strings.TrimSpace(key)
	if unit == "" || key == "" {
		return errors.New("unit and key are required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO unit_settings (unit, key, value, updated_at) VALUES ($1, $2, $3, now())
		 ON CONFLICT (unit, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		unit, key, value)
	return err
}

// LoadUnitSettings returns a unit's settings as a key→value map (for the runtime
// settings holder).
func (s *Store) LoadUnitSettings(ctx context.Context, unit string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM unit_settings WHERE unit = $1`, unit)
	if err != nil {
		return nil, fmt.Errorf("load unit settings: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// ListUnitSettings returns a unit's settings rows (for the HTTP API), ordered by
// key.
func (s *Store) ListUnitSettings(ctx context.Context, unit string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value, updated_at FROM unit_settings WHERE unit = $1 ORDER BY key`, unit)
	if err != nil {
		return nil, fmt.Errorf("list unit settings: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var key, value string
		var updatedAt any
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"key": key, "value": value, "updated_at": updatedAt})
	}
	return out, rows.Err()
}

// ListenForUnitSettingsChanges invokes onChange (with the affected unit) on every
// (re)connect and on every unit_settings NOTIFY, so per-unit setting edits apply
// instantly.
func (s *Store) ListenForUnitSettingsChanges(ctx context.Context, onChange func(unit string)) {
	for ctx.Err() == nil {
		if err := s.listenChannel(ctx, unitSettingsChangeChannel, onChange); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}
