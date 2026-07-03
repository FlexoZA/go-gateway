package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// settings.go backs the editable server_settings (key/value) store. The first
// setting is webhook_url — the external endpoint that stores all GPS/event data.

// SettingWebhookURL is the key for the GPS/event telemetry webhook URL.
const SettingWebhookURL = "webhook_url"

// SettingDevicePort is the configured device TCP port (applied on next start).
// SettingDevicePortActive is the port the gateway actually bound on this start —
// written by the gateway at startup so the UI can flag a pending restart.
const (
	SettingDevicePort       = "device_port"
	SettingDevicePortActive = "device_port_active"
)

// SettingGatewayName is the gateway identifier embedded in every universal
// message's "gateway" field (applied live).
const SettingGatewayName = "gateway_name"

// SettingDeviceRejectUnknown controls device authorization: "true" quarantines
// and rejects unknown serials (require approval); "false" auto-provisions and
// admits them. Applied live.
const SettingDeviceRejectUnknown = "device_reject_unknown"

// SettingMediaRetentionDays is how many days stored clips and snapshots are kept
// before the retention reaper deletes them (files + rows). "0" keeps them forever.
// Edited live in the admin Server Settings; applied on the next hourly sweep.
const SettingMediaRetentionDays = "media_retention_days"

// GetSetting returns a setting's value and whether the row exists.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM server_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SeedSettingDefault inserts a setting only if it does not already exist, so an
// env-provided default seeds the row once and later edits are never overwritten.
func (s *Store) SeedSettingDefault(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO server_settings (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO NOTHING`, key, value)
	return err
}

// SetSetting upserts a setting value. Firing the NOTIFY makes the running
// gateway apply it within milliseconds.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("setting key is required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO server_settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	return err
}

// ListSettings returns all settings, ordered by key.
func (s *Store) ListSettings(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value, updated_at FROM server_settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
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

// ListenForSettingsChanges invokes onChange on every (re)connect and on every
// server_settings NOTIFY, so setting edits apply instantly.
func (s *Store) ListenForSettingsChanges(ctx context.Context, onChange func(key string)) {
	for ctx.Err() == nil {
		if err := s.listenChannel(ctx, settingsChangeChannel, onChange); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}
