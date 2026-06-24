package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dfm/device-gateway/internal/core/mapping"
)

// admin.go holds the read/write methods that back the management HTTP API (the
// admin panel). Everything the panel needs goes through these — the panel never
// touches the database directly. List methods return JSON-ready maps so the
// httpapi layer stays storage-agnostic.

// ErrNotFound is returned when an addressed row does not exist.
var ErrNotFound = errors.New("not found")

// ListDevices returns the known/approved device registry, newest activity first.
func (s *Store) ListDevices(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT serial, COALESCE(protocol, ''), COALESCE(status, ''), first_seen_at, last_seen_at
		 FROM devices ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var serial, protocol, status string
		var firstSeen, lastSeen any
		if err := rows.Scan(&serial, &protocol, &status, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"serial":        serial,
			"protocol":      protocol,
			"status":        status,
			"first_seen_at": firstSeen,
			"last_seen_at":  lastSeen,
		})
	}
	return out, rows.Err()
}

// ListPendingDevices returns devices awaiting approval (quarantined unknowns).
// Only populated when DEVICE_REJECT_UNKNOWN=true.
func (s *Store) ListPendingDevices(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT serial, COALESCE(source_protocol_guess, ''), COALESCE(remote_ip, ''),
		        last_payload_meta, last_seen_at, COALESCE(status, '')
		 FROM unknown_devices ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list pending devices: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var serial, protoGuess, remoteIP, status string
		var meta any
		var lastSeen any
		if err := rows.Scan(&serial, &protoGuess, &remoteIP, &meta, &lastSeen, &status); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"serial":         serial,
			"protocol_guess": protoGuess,
			"remote_ip":      remoteIP,
			"meta":           meta,
			"last_seen_at":   lastSeen,
			"status":         status,
		})
	}
	return out, rows.Err()
}

// ApproveDevice whitelists a serial: it is inserted into the devices registry
// (so it is admitted even under DEVICE_REJECT_UNKNOWN) and removed from the
// quarantine. The protocol guess from quarantine is carried over when present.
// fallbackProtocol is used when no guess was recorded.
func (s *Store) ApproveDevice(ctx context.Context, serial, fallbackProtocol string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return errors.New("serial required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var protoGuess *string
	err = tx.QueryRow(ctx, `SELECT source_protocol_guess FROM unknown_devices WHERE serial = $1`, serial).Scan(&protoGuess)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Not quarantined; approving a brand-new serial directly is still allowed.
	case err != nil:
		return err
	}
	protocol := fallbackProtocol
	if protoGuess != nil && *protoGuess != "" {
		protocol = *protoGuess
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO devices (serial, protocol, status) VALUES ($1, $2, 'offline')
		 ON CONFLICT (serial) DO UPDATE SET protocol = COALESCE(EXCLUDED.protocol, devices.protocol), last_seen_at = now()`,
		serial, nullIfEmpty(protocol)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM unknown_devices WHERE serial = $1`, serial); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RejectDevice removes a serial from the quarantine without whitelisting it.
func (s *Store) RejectDevice(ctx context.Context, serial string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return errors.New("serial required")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM unknown_devices WHERE serial = $1`, serial)
	return err
}

// DeleteDevice removes a serial from the approved registry. Under
// DEVICE_REJECT_UNKNOWN it will be quarantined again on its next connection.
func (s *Store) DeleteDevice(ctx context.Context, serial string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return errors.New("serial required")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE serial = $1`, serial)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEventMappings returns the editable mapping rows for a unit and model (empty
// model = the unit-wide default), ordered for stable display.
func (s *Store) ListEventMappings(ctx context.Context, unit, model string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, unit, model, map_type, code, event_code, COALESCE(description, ''), updated_at
		 FROM event_mappings WHERE unit = $1 AND model = $2 ORDER BY map_type, code`, unit, model)
	if err != nil {
		return nil, fmt.Errorf("list event mappings: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var u, m, mapType, eventCode, description string
		var code int
		var updatedAt any
		if err := rows.Scan(&id, &u, &m, &mapType, &code, &eventCode, &description, &updatedAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":          id,
			"unit":        u,
			"model":       m,
			"map_type":    mapType,
			"code":        code,
			"event_code":  eventCode,
			"description": description,
			"updated_at":  updatedAt,
		})
	}
	return out, rows.Err()
}

// ListEventMappingModels returns the distinct non-empty models that have mappings
// for a unit (so the admin can list per-model tables).
func (s *Store) ListEventMappingModels(ctx context.Context, unit string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT model FROM event_mappings WHERE unit = $1 AND model <> '' ORDER BY model`, unit)
	if err != nil {
		return nil, fmt.Errorf("list mapping models: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CopyEventMappings copies every row from one model to another for a unit (used to
// seed a new model's table from the default). Existing rows in the target are kept.
func (s *Store) CopyEventMappings(ctx context.Context, unit, fromModel, toModel string) error {
	if strings.TrimSpace(unit) == "" || fromModel == toModel {
		return errors.New("unit required and source/target models must differ")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO event_mappings (unit, model, map_type, code, event_code, description)
		 SELECT unit, $3, map_type, code, event_code, description
		 FROM event_mappings WHERE unit = $1 AND model = $2
		 ON CONFLICT (unit, model, map_type, code) DO NOTHING`,
		unit, fromModel, toModel)
	return err
}

// UpsertEventMapping inserts or updates one mapping row. The change fires the
// event_mappings NOTIFY, so the running gateway reloads it within milliseconds.
func (s *Store) UpsertEventMapping(ctx context.Context, unit string, e mapping.Entry) error {
	unit = strings.TrimSpace(unit)
	if unit == "" || strings.TrimSpace(e.MapType) == "" || strings.TrimSpace(e.EventCode) == "" {
		return errors.New("unit, map_type and event_code are required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO event_mappings (unit, model, map_type, code, event_code, description, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now())
		 ON CONFLICT (unit, model, map_type, code) DO UPDATE
		   SET event_code = EXCLUDED.event_code, description = EXCLUDED.description, updated_at = now()`,
		unit, e.Model, e.MapType, e.Code, e.EventCode, nullIfEmpty(e.Description))
	return err
}

// DeleteEventMapping removes one mapping row (reverting that code to the built-in
// default). Fires the NOTIFY for an instant reload.
func (s *Store) DeleteEventMapping(ctx context.Context, unit, model, mapType string, code int) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM event_mappings WHERE unit = $1 AND model = $2 AND map_type = $3 AND code = $4`,
		unit, model, mapType, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGatewayErrors returns recent system/server errors, newest first.
func (s *Store) ListGatewayErrors(ctx context.Context, limit, offset int) ([]map[string]any, error) {
	limit, offset = clampPage(limit, offset)
	rows, err := s.pool.Query(ctx,
		`SELECT id, COALESCE(unit, ''), COALESCE(namespace, ''), COALESCE(event, ''),
		        message, fields, created_at
		 FROM gateway_errors ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list gateway errors: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var unit, namespace, event, message string
		var fields, createdAt any
		if err := rows.Scan(&id, &unit, &namespace, &event, &message, &fields, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":         id,
			"unit":       unit,
			"namespace":  namespace,
			"event":      event,
			"message":    message,
			"fields":     fields,
			"created_at": createdAt,
		})
	}
	return out, rows.Err()
}

// ListDeviceErrors returns recent device-reported errors, newest first.
func (s *Store) ListDeviceErrors(ctx context.Context, limit, offset int) ([]map[string]any, error) {
	limit, offset = clampPage(limit, offset)
	rows, err := s.pool.Query(ctx,
		`SELECT id, serial, COALESCE(error_category, ''), error_message,
		        COALESCE(remote_address, ''), COALESCE(remote_port, 0), raw_payload, created_at
		 FROM device_errors ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list device errors: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var serial, category, message, remoteAddr string
		var remotePort int
		var raw, createdAt any
		if err := rows.Scan(&id, &serial, &category, &message, &remoteAddr, &remotePort, &raw, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":             id,
			"serial":         serial,
			"error_category": category,
			"error_message":  message,
			"remote_address": remoteAddr,
			"remote_port":    remotePort,
			"raw_payload":    raw,
			"created_at":     createdAt,
		})
	}
	return out, rows.Err()
}

// clampPage bounds pagination parameters to safe values.
func clampPage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
