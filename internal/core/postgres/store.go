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
	"errors"
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

// settingsChangeChannel is the LISTEN/NOTIFY channel fired by the server_settings
// trigger whenever a setting changes.
const settingsChangeChannel = "server_settings_changed"

// webhooksChangeChannel is the LISTEN/NOTIFY channel fired by the webhooks
// trigger whenever a telemetry webhook is added, edited, toggled, or removed.
const webhooksChangeChannel = "webhooks_changed"

// unitSettingsChangeChannel is the LISTEN/NOTIFY channel fired by the
// unit_settings trigger (carrying the affected unit) whenever a per-unit-type
// setting changes, so the running gateway hot-reloads that unit's settings.
const unitSettingsChangeChannel = "unit_settings_changed"

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
	// Front-end-editable event mappings: per unit AND device model, a named map_type
	// maps a raw device code to an ACM Standard Event Code. Seeded from built-in
	// defaults under the empty model (the unit-wide default that applies to any model
	// without its own table).
	`CREATE TABLE IF NOT EXISTS event_mappings (
		id          BIGSERIAL PRIMARY KEY,
		unit        TEXT NOT NULL,
		model       TEXT NOT NULL DEFAULT '',
		map_type    TEXT NOT NULL,
		code        INTEGER NOT NULL,
		event_code  TEXT NOT NULL,
		description TEXT,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	// Migrate older tables that predate the model column, and move the uniqueness to
	// include model (a unique index is idempotent, unlike ADD CONSTRAINT).
	`ALTER TABLE event_mappings ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE event_mappings DROP CONSTRAINT IF EXISTS event_mappings_unit_map_type_code_key`,
	`CREATE UNIQUE INDEX IF NOT EXISTS event_mappings_unit_model_map_type_code
		ON event_mappings (unit, model, map_type, code)`,
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
	// Per-unit-type gateway settings (key/value, scoped by unit). Distinct from the
	// global server_settings and from per-device parameter config: these are a
	// unit's own editable settings (e.g. a GPS tracker's timezone offset), declared
	// by the unit's SettingsSchema and seeded from its defaults. Changes apply to
	// the running gateway instantly via NOTIFY (carrying the unit).
	`CREATE TABLE IF NOT EXISTS unit_settings (
		unit       TEXT NOT NULL,
		key        TEXT NOT NULL,
		value      TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (unit, key)
	)`,
	`CREATE OR REPLACE FUNCTION notify_unit_settings_changed() RETURNS trigger AS $$
	BEGIN
		PERFORM pg_notify('unit_settings_changed', COALESCE(NEW.unit, OLD.unit));
		RETURN NULL;
	END;
	$$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS unit_settings_notify ON unit_settings`,
	`CREATE TRIGGER unit_settings_notify
		AFTER INSERT OR UPDATE OR DELETE ON unit_settings
		FOR EACH ROW EXECUTE FUNCTION notify_unit_settings_changed()`,
	// Recorded video clips pulled from a device's SD card (H-Protocol playback,
	// 0x4070). The .mp4 is stored on the server (CLIPS_ROOT, the "bucket");
	// storage_path is relative to that root. status: requested → receiving →
	// ready | error. This is metadata only — the bytes live on disk.
	`CREATE TABLE IF NOT EXISTS clips (
		id             BIGSERIAL PRIMARY KEY,
		serial         TEXT NOT NULL,
		camera         INT NOT NULL,
		profile        INT NOT NULL,
		start_utc      BIGINT NOT NULL,
		end_utc        BIGINT NOT NULL,
		duration_secs  INT NOT NULL DEFAULT 0,
		status         TEXT NOT NULL DEFAULT 'requested',
		file_size      BIGINT NOT NULL DEFAULT 0,
		bytes_received BIGINT NOT NULL DEFAULT 0,
		storage_path   TEXT NOT NULL,
		error          TEXT NOT NULL DEFAULT '',
		created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS clips_serial_created_idx ON clips (serial, created_at DESC)`,
	// Snapshots saved to the gateway: a single JPEG persisted under
	// CLIPS_ROOT/snapshots; storage_path is relative to that root. source is
	// 'capture' (taken on demand) or 'device' (copied off the device's SD card).
	`CREATE TABLE IF NOT EXISTS snapshots (
		id           BIGSERIAL PRIMARY KEY,
		serial       TEXT NOT NULL,
		camera       INT NOT NULL DEFAULT 0,
		kind         TEXT NOT NULL DEFAULT 'general',
		source       TEXT NOT NULL DEFAULT 'capture',
		captured_utc BIGINT NOT NULL DEFAULT 0,
		device_path  TEXT NOT NULL DEFAULT '',
		storage_path TEXT NOT NULL,
		file_size    BIGINT NOT NULL DEFAULT 0,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS snapshots_serial_created_idx ON snapshots (serial, created_at DESC)`,
	// Durable webhook delivery queue. Holds telemetry only transiently — one row per
	// pending POST to an external endpoint, deleted on delivery — so a webhook outage
	// or a gateway restart does not lose GPS/event data in flight. See outbox.go.
	`CREATE TABLE IF NOT EXISTS webhook_outbox (
		id              BIGSERIAL PRIMARY KEY,
		target          TEXT NOT NULL,
		body            BYTEA NOT NULL,
		attempts        INT NOT NULL DEFAULT 0,
		last_error      TEXT NOT NULL DEFAULT '',
		created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
		next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS webhook_outbox_due ON webhook_outbox (next_attempt_at)`,
	// One-time migration: the JT808 plugin was split into make/model unit types
	// (each vendor/model on its own port), so the original generic "jt808" unit
	// became "dfm-n62" (the unbranded N62 group). Rename its rows across the device
	// registry, event mappings, per-unit settings, and the unit-scoped settings keys
	// (device_port:<unit>, cap_<feature>:<unit>). Idempotent: after the first apply
	// there are no "jt808" rows left, so these no-op. Runs after all tables exist.
	`UPDATE devices SET protocol = 'dfm-n62' WHERE protocol = 'jt808'`,
	`UPDATE unknown_devices SET source_protocol_guess = 'dfm-n62' WHERE source_protocol_guess = 'jt808'`,
	`UPDATE event_mappings SET unit = 'dfm-n62' WHERE unit = 'jt808'`,
	`UPDATE unit_settings SET unit = 'dfm-n62' WHERE unit = 'jt808'`,
	`UPDATE server_settings SET key = replace(key, ':jt808', ':dfm-n62')
		WHERE key LIKE '%:jt808'
		  AND replace(key, ':jt808', ':dfm-n62') NOT IN (SELECT key FROM server_settings)`,
}

// Store is the gateway database. It backs device authorization (the unit
// registry); it is NOT a telemetry sink.
type Store struct {
	pool *pgxpool.Pool
	// listenCfg is a standalone connection config (derived from the pool's DSN)
	// for opening DEDICATED LISTEN/NOTIFY connections. Long-lived LISTEN
	// connections must not come from the query pool — see listenChannel.
	listenCfg *pgx.ConnConfig
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
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	// Derive a standalone (non-pool) connection config for LISTEN/NOTIFY. pgxpool
	// has already stripped pool_* params into cfg, so ConnConfig is a clean
	// single-connection config.
	s := &Store{pool: pool, listenCfg: cfg.ConnConfig.Copy()}
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

// Ping verifies database connectivity, for the /healthz check.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

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
	if !errors.Is(err, pgx.ErrNoRows) {
		// Real query error (not just "device not found").
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
			`INSERT INTO event_mappings (unit, model, map_type, code, event_code, description)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (unit, model, map_type, code) DO NOTHING`,
			unit, e.Model, e.MapType, e.Code, e.EventCode, nullIfEmpty(e.Description),
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

// listenChannel opens a DEDICATED connection (not from the query pool) and
// LISTENs on the given channel, calling onChange once on connect (resync) and
// again on every notification. Returns on error so the caller can reconnect.
//
// It must not borrow from the pool: a LISTEN connection lives for the whole
// process, and there are many listeners — per-unit mappings and per-unit
// settings plus the global webhooks/server-settings channels — so with several
// units (e.g. howen + fleetiger + cathexis) hijacking pool connections would
// shrink the query pool well below its modest default and could starve device
// authorization. A standalone connection keeps the pool fully available.
func (s *Store) listenChannel(ctx context.Context, channel string, onChange func(payload string)) error {
	conn, err := pgx.ConnectConfig(ctx, s.listenCfg)
	if err != nil {
		return err
	}
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

// LoadEventMappings returns a unit's current mappings grouped by model then
// map_type. The empty-model entry ("") is the unit-wide default.
func (s *Store) LoadEventMappings(ctx context.Context, unit string) (mapping.ByModel, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT model, map_type, code, event_code FROM event_mappings WHERE unit = $1`, unit)
	if err != nil {
		return nil, fmt.Errorf("load event mappings: %w", err)
	}
	defer rows.Close()

	out := mapping.ByModel{}
	for rows.Next() {
		var model, mapType, eventCode string
		var code int
		if err := rows.Scan(&model, &mapType, &code, &eventCode); err != nil {
			return nil, err
		}
		if out[model] == nil {
			out[model] = mapping.Table{}
		}
		if out[model][mapType] == nil {
			out[model][mapType] = map[int]string{}
		}
		out[model][mapType][code] = eventCode
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
