package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// webhooks.go backs the telemetry webhooks — the external endpoints that store
// all GPS/event data. There may be several, each independently enabled/disabled.
// The gateway loads the enabled URLs into the webhook sink and reloads on change.

// ListWebhooks returns all configured webhooks, newest first.
func (s *Store) ListWebhooks(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, url, is_enabled, created_at, updated_at
		 FROM webhooks ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, url string
		var enabled bool
		var createdAt, updatedAt any
		if err := rows.Scan(&id, &name, &url, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "url": url, "is_enabled": enabled,
			"created_at": createdAt, "updated_at": updatedAt,
		})
	}
	return out, rows.Err()
}

// CreateWebhook adds a webhook. Inserting a URL that already exists updates that
// row's name/enabled instead of failing (so the UI's "add" is idempotent).
func (s *Store) CreateWebhook(ctx context.Context, name, url string, enabled bool) (int64, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return 0, errors.New("url is required")
	}
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO webhooks (name, url, is_enabled) VALUES ($1, $2, $3)
		 ON CONFLICT (url) DO UPDATE SET name = EXCLUDED.name, is_enabled = EXCLUDED.is_enabled, updated_at = now()
		 RETURNING id`,
		name, url, enabled).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create webhook: %w", err)
	}
	return id, nil
}

// UpdateWebhook updates a webhook's name, URL, and enabled flag by id.
func (s *Store) UpdateWebhook(ctx context.Context, id int64, name, url string, enabled bool) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("url is required")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE webhooks SET name = $2, url = $3, is_enabled = $4, updated_at = now() WHERE id = $1`,
		id, name, url, enabled)
	if err != nil {
		return fmt.Errorf("update webhook: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWebhook removes a webhook by id.
func (s *Store) DeleteWebhook(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LoadEnabledWebhookURLs returns the URLs of all enabled webhooks, for the sink.
func (s *Store) LoadEnabledWebhookURLs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT url FROM webhooks WHERE is_enabled ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load enabled webhooks: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		out = append(out, url)
	}
	return out, rows.Err()
}

// CountWebhooks returns how many webhooks exist (used to decide whether to seed).
func (s *Store) CountWebhooks(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM webhooks`).Scan(&n)
	return n, err
}

// SeedWebhookIfEmpty inserts a single enabled webhook when none exist yet — used
// to migrate the original single-URL config (env / legacy webhook_url setting)
// into the multi-webhook model. No-op when a webhook already exists or url empty.
func (s *Store) SeedWebhookIfEmpty(ctx context.Context, name, url string) (bool, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return false, nil
	}
	n, err := s.CountWebhooks(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if _, err := s.CreateWebhook(ctx, name, url, true); err != nil {
		return false, err
	}
	return true, nil
}

// ListenForWebhookChanges invokes onChange on every (re)connect and on every
// webhooks NOTIFY, so webhook edits apply instantly.
func (s *Store) ListenForWebhookChanges(ctx context.Context, onChange func(payload string)) {
	for ctx.Err() == nil {
		if err := s.listenChannel(ctx, webhooksChangeChannel, onChange); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}
