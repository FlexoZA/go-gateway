// Package postgres is the gateway's OWN database — not a telemetry store.
//
// GPS and event data are never saved here; that telemetry goes to the universal
// webhook (the external database that stores all GPS/event data). This package
// holds gateway-side state: the unit registry used to verify connecting devices
// (replacing the old Supabase test table) and, in future, server-settings tables.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

// mappingChangeChannel is the LISTEN/NOTIFY channel fired by the event_mappings
// trigger whenever a mapping row is inserted, updated, or deleted.
const mappingChangeChannel = "event_mappings_changed"

// workflowChangeChannel is the LISTEN/NOTIFY channel fired by the
// mapping_workflows trigger whenever a per-model workflow changes.
const workflowChangeChannel = "mapping_workflows_changed"

// settingsChangeChannel is the LISTEN/NOTIFY channel fired by the server_settings
// trigger whenever a setting changes.
const settingsChangeChannel = "server_settings_changed"

// webhooksChangeChannel is the LISTEN/NOTIFY channel fired by the webhooks
// trigger whenever a telemetry webhook is added, edited, toggled, or removed.
const webhooksChangeChannel = "webhooks_changed"

// schema is applied idempotently on startup. Add server-settings tables here as
// they are designed.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS devices (
		serial        TEXT PRIMARY KEY,
		protocol      TEXT,
		status        TEXT,
		first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS unknown_devices (
		serial                TEXT PRIMARY KEY,
		source_protocol_guess TEXT,
		remote_ip             TEXT,
		last_payload_meta     JSONB,
		last_seen_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
		status                TEXT DEFAULT 'quarantined'
	)`,
	// Front-end-editable event mappings: per unit, a named map_type maps a raw
	// device code to an ACM Standard Event Code. Seeded from built-in defaults.
	`CREATE TABLE IF NOT EXISTS event_mappings (
		id          BIGSERIAL PRIMARY KEY,
		unit        TEXT NOT NULL,
		map_type    TEXT NOT NULL,
		code        INTEGER NOT NULL,
		event_code  TEXT NOT NULL,
		description TEXT,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
		UNIQUE (unit, map_type, code)
	)`,
	// Fire a NOTIFY (carrying the unit) on any change so gateways reload mappings
	// instantly instead of waiting for the periodic refresh.
	`CREATE OR REPLACE FUNCTION notify_event_mappings_changed() RETURNS trigger AS $$
	BEGIN
		PERFORM pg_notify('event_mappings_changed', COALESCE(NEW.unit, OLD.unit));
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS event_mappings_notify ON event_mappings`,
	`CREATE TRIGGER event_mappings_notify
		AFTER INSERT OR UPDATE OR DELETE ON event_mappings
		FOR EACH ROW EXECUTE FUNCTION notify_event_mappings_changed()`,
	// Front-end user accounts. Passwords are never stored — only a bcrypt hash
	// (which embeds a per-user salt). No roles for now: a flat list of users.
	`CREATE TABLE IF NOT EXISTS users (
		id            BIGSERIAL PRIMARY KEY,
		email         TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		is_active     BOOLEAN NOT NULL DEFAULT true,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
		last_login_at TIMESTAMPTZ
	)`,
	// Case-insensitive uniqueness without the citext extension.
	`CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower ON users (lower(email))`,
	// API keys for the HTTP API. Only a sha256 hash of the key is stored; the
	// plaintext key is shown once at creation and never persisted.
	`CREATE TABLE IF NOT EXISTS api_keys (
		id           BIGSERIAL PRIMARY KEY,
		name         TEXT NOT NULL DEFAULT '',
		key_hash     TEXT NOT NULL UNIQUE,
		prefix       TEXT NOT NULL,
		is_active    BOOLEAN NOT NULL DEFAULT true,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
		last_used_at TIMESTAMPTZ,
		expires_at   TIMESTAMPTZ
	)`,
	// System/server/gateway errors: every Error-level log line, persisted via the
	// logger's error sink. `fields` keeps the full structured payload; the common
	// columns are lifted out for indexing and quick filtering.
	`CREATE TABLE IF NOT EXISTS gateway_errors (
		id         BIGSERIAL PRIMARY KEY,
		unit       TEXT,
		namespace  TEXT,
		event      TEXT,
		message    TEXT NOT NULL DEFAULT '',
		fields     JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS gateway_errors_created_at ON gateway_errors (created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS gateway_errors_event ON gateway_errors (event)`,
	// Device-reported errors: problems a device tells us about over its connection
	// (e.g. media/clip/event upload failures). Mirrors the old mvr_device_errors.
	`CREATE TABLE IF NOT EXISTS device_errors (
		id             BIGSERIAL PRIMARY KEY,
		serial         TEXT NOT NULL,
		error_category TEXT,
		error_message  TEXT NOT NULL,
		remote_address TEXT,
		remote_port    INTEGER,
		raw_payload    JSONB,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS device_errors_serial_created_at ON device_errors (serial, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS device_errors_category ON device_errors (error_category)`,
	// Per-model device-mapping workflows: the visual ("N8N-style") node graph that
	// maps a model's incoming frames to event codes. Strictly per (unit, model) —
	// a device uses its own model's active workflow or none. `graph` is the React
	// Flow node/edge JSON the engine (internal/core/flow) interprets.
	`CREATE TABLE IF NOT EXISTS mapping_workflows (
		id         BIGSERIAL PRIMARY KEY,
		unit       TEXT NOT NULL,
		model      TEXT NOT NULL,
		name       TEXT NOT NULL DEFAULT '',
		graph      JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
		is_active  BOOLEAN NOT NULL DEFAULT true,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		UNIQUE (unit, model)
	)`,
	// Hot-reload: fire a NOTIFY (carrying the unit) on any change so gateways
	// reload workflows instantly, exactly like event_mappings.
	`CREATE OR REPLACE FUNCTION notify_mapping_workflows_changed() RETURNS trigger AS $$
	BEGIN
		PERFORM pg_notify('mapping_workflows_changed', COALESCE(NEW.unit, OLD.unit));
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS mapping_workflows_notify ON mapping_workflows`,
	`CREATE TRIGGER mapping_workflows_notify
		AFTER INSERT OR UPDATE OR DELETE ON mapping_workflows
		FOR EACH ROW EXECUTE FUNCTION notify_mapping_workflows_changed()`,
	// Canonical ACM Standard Event Codes — the picklist the front end offers when
	// choosing an event_code. Seeded on startup from the embedded CSV
	// (internal/core/eventcodes); rows not in the CSV are preserved so custom
	// codes can be added later.
	`CREATE TABLE IF NOT EXISTS standard_event_codes (
		code         TEXT PRIMARY KEY,
		category     TEXT,
		notes        TEXT,
		device_notes TEXT,
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	// Editable server settings (key/value). First setting: webhook_url — the
	// external endpoint that stores all GPS/event data. Changes apply to the
	// running gateway instantly via NOTIFY.
	`CREATE TABLE IF NOT EXISTS server_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE OR REPLACE FUNCTION notify_server_settings_changed() RETURNS trigger AS $$
	BEGIN
		PERFORM pg_notify('server_settings_changed', COALESCE(NEW.key, OLD.key));
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS server_settings_notify ON server_settings`,
	`CREATE TRIGGER server_settings_notify
		AFTER INSERT OR UPDATE OR DELETE ON server_settings
		FOR EACH ROW EXECUTE FUNCTION notify_server_settings_changed()`,
	// Telemetry webhooks: the (possibly multiple) external endpoints that store all
	// GPS/event data. Each can be enabled/disabled independently. Changes apply to
	// the running gateway instantly via NOTIFY.
	`CREATE TABLE IF NOT EXISTS webhooks (
		id         BIGSERIAL PRIMARY KEY,
		name       TEXT NOT NULL DEFAULT '',
		url        TEXT NOT NULL UNIQUE,
		is_enabled BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE OR REPLACE FUNCTION notify_webhooks_changed() RETURNS trigger AS $$
	BEGIN
		PERFORM pg_notify('webhooks_changed', '');
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS webhooks_notify ON webhooks`,
	`CREATE TRIGGER webhooks_notify
		AFTER INSERT OR UPDATE OR DELETE ON webhooks
		FOR EACH ROW EXECUTE FUNCTION notify_webhooks_changed()`,
}

// Store is the gateway database. It backs device authorization (the unit
// registry); it is NOT a telemetry sink.
type Store struct {
	pool *pgxpool.Pool
	// rejectUnknown mirrors the old Supabase gate: when true, a serial absent
	// from `devices` is quarantined and rejected. When false (default), unknown
	// serials are auto-provisioned and admitted. It is an atomic so it can be
	// toggled live from the admin panel (read on every device connection).
	rejectUnknown atomic.Bool
}

// SetRejectUnknown toggles whether unknown serials are quarantined (true) or
// auto-provisioned and admitted (false). Safe to call concurrently with Authorize.
func (s *Store) SetRejectUnknown(v bool) { s.rejectUnknown.Store(v) }

// RejectUnknown reports the current setting.
func (s *Store) RejectUnknown() bool { return s.rejectUnknown.Load() }

// New connects to PostgreSQL, applies the schema, and returns the store.
func New(ctx context.Context, dsn string, rejectUnknown bool) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	s := &Store{pool: pool}
	s.rejectUnknown.Store(rejectUnknown)
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres migrate: %w", err)
		}
	}
	return nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Authorize implements device.Authenticator. Known serials are admitted and
// touched; unknown serials are auto-provisioned and admitted unless
// rejectUnknown is set, in which case they are quarantined and rejected.
func (s *Store) Authorize(ctx context.Context, info device.RegisterInfo) (device.AuthResult, error) {
	var protocol *string
	err := s.pool.QueryRow(ctx, `SELECT protocol FROM devices WHERE serial = $1`, info.Serial).Scan(&protocol)
	if err == nil {
		// Known device — touch last_seen.
		_, _ = s.pool.Exec(ctx, `UPDATE devices SET last_seen_at = now() WHERE serial = $1`, info.Serial)
		p := info.Protocol
		if protocol != nil && *protocol != "" {
			p = *protocol
		}
		return device.AuthResult{Known: true, Protocol: p}, nil
	}
	if err.Error() != "no rows in result set" {
		// Real query error.
		return device.AuthResult{}, fmt.Errorf("device lookup: %w", err)
	}

	// Unknown device.
	if s.rejectUnknown.Load() {
		meta, _ := json.Marshal(info.Meta)
		_, _ = s.pool.Exec(ctx,
			`INSERT INTO unknown_devices (serial, source_protocol_guess, remote_ip, last_payload_meta, last_seen_at, status)
			 VALUES ($1, $2, $3, $4, now(), 'quarantined')
			 ON CONFLICT (serial) DO UPDATE SET last_seen_at = now(), remote_ip = EXCLUDED.remote_ip, last_payload_meta = EXCLUDED.last_payload_meta`,
			info.Serial, nullIfEmpty(info.Protocol), nullIfEmpty(info.RemoteIP), string(meta),
		)
		return device.AuthResult{Known: false}, nil
	}

	// Auto-provision and admit.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO devices (serial, protocol, status) VALUES ($1, $2, 'online')
		 ON CONFLICT (serial) DO UPDATE SET last_seen_at = now()`,
		info.Serial, nullIfEmpty(info.Protocol),
	)
	if err != nil {
		return device.AuthResult{}, fmt.Errorf("device provision: %w", err)
	}
	return device.AuthResult{Known: true, Protocol: info.Protocol}, nil
}

// UpdateStatus records a device lifecycle status.
func (s *Store) UpdateStatus(ctx context.Context, serial, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE devices SET status = $2, last_seen_at = now() WHERE serial = $1`, serial, status)
	return err
}

// SeedEventMappings inserts a unit's built-in default mappings, skipping any
// (unit, map_type, code) that already exists. This populates the table for the
// front end on first run without overwriting later edits.
func (s *Store) SeedEventMappings(ctx context.Context, unit string, entries []mapping.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO event_mappings (unit, map_type, code, event_code, description)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (unit, map_type, code) DO NOTHING`,
			unit, e.MapType, e.Code, e.EventCode, nullIfEmpty(e.Description),
		)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("seed event mappings: %w", err)
		}
	}
	return nil
}

// ListenForMappingChanges blocks until ctx is cancelled, invoking onChange every
// time an event_mappings row changes (via Postgres LISTEN/NOTIFY) so edits apply
// instantly. The notification payload is the affected unit. onChange is also
// called once on every (re)connect so edits made while disconnected are not
// missed. Reconnects automatically with a short backoff.
func (s *Store) ListenForMappingChanges(ctx context.Context, onChange func(unit string)) {
	for ctx.Err() == nil {
		if err := s.listenChannel(ctx, mappingChangeChannel, onChange); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// listenChannel hijacks a pooled connection, LISTENs on the given channel, calls
// onChange once on connect (resync) and again on every notification. Returns on
// error so the caller can reconnect. Shared by the mapping and workflow channels.
func (s *Store) listenChannel(ctx context.Context, channel string, onChange func(payload string)) error {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// Hijack removes the connection from the pool — we own it for the lifetime of
	// this LISTEN and must close it ourselves.
	conn := pooled.Hijack()
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	// Resync on connect to catch edits made while we were disconnected.
	onChange("")
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		onChange(n.Payload)
	}
}

// LoadEventMappings returns a unit's current mappings grouped by map_type.
func (s *Store) LoadEventMappings(ctx context.Context, unit string) (mapping.Table, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT map_type, code, event_code FROM event_mappings WHERE unit = $1`, unit)
	if err != nil {
		return nil, fmt.Errorf("load event mappings: %w", err)
	}
	defer rows.Close()

	out := mapping.Table{}
	for rows.Next() {
		var mapType, eventCode string
		var code int
		if err := rows.Scan(&mapType, &code, &eventCode); err != nil {
			return nil, err
		}
		if out[mapType] == nil {
			out[mapType] = map[int]string{}
		}
		out[mapType][code] = eventCode
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
