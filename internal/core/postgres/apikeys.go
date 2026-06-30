package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// apiKeyPrefix tags generated keys so they're recognizable (e.g. in logs/UI).
const apiKeyPrefix = "dgw_"

// apiKeyDisplayLen is how many leading characters are kept as the non-secret
// display prefix for a management UI.
const apiKeyDisplayLen = 12

// APIKeyInfo is the non-secret metadata about a key (never includes the key).
type APIKeyInfo struct {
	Name       string
	Prefix     string
	IsActive   bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// GenerateAPIKey returns a new random key (plaintext, shown once), its sha256
// hash (stored), and its display prefix.
func GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 32) // 256 bits of entropy
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	hash = hashAPIKey(plaintext)
	prefix = plaintext
	if len(prefix) > apiKeyDisplayLen {
		prefix = prefix[:apiKeyDisplayLen]
	}
	return plaintext, hash, prefix, nil
}

// hashAPIKey returns the hex sha256 of a key. API keys are high-entropy random
// tokens, so a fast cryptographic hash is appropriate (unlike user passwords).
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateAPIKey mints a new key with an optional name and returns the plaintext
// (the only time it is ever available — store only the hash).
func (s *Store) CreateAPIKey(ctx context.Context, name string) (string, error) {
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO api_keys (name, key_hash, prefix) VALUES ($1, $2, $3)`,
		name, hash, prefix); err != nil {
		return "", err
	}
	return plaintext, nil
}

// VerifyAPIKey reports whether a presented key is valid (active and unexpired).
// It looks up by hash, so the plaintext is never compared in the app. On success
// it records last_used_at.
func (s *Store) VerifyAPIKey(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	hash := hashAPIKey(key)
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM api_keys
		 WHERE key_hash = $1 AND is_active AND (expires_at IS NULL OR expires_at > now())`,
		hash).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, id)
	return true, nil
}

// RevokeAPIKey deactivates all keys matching the given display prefix. Returns
// the number revoked.
func (s *Store) RevokeAPIKey(ctx context.Context, prefix string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET is_active = false WHERE prefix = $1 AND is_active`, prefix)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteAPIKey permanently removes a revoked (inactive) key by display prefix.
// Active keys are left untouched — they must be revoked first — so a single
// errant call can't silently cut off a live integration. Returns the number
// deleted.
func (s *Store) DeleteAPIKey(ctx context.Context, prefix string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM api_keys WHERE prefix = $1 AND NOT is_active`, prefix)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListAPIKeyMeta returns non-secret key metadata as JSON-ready maps (for the
// admin API). It never includes the key or its hash.
func (s *Store) ListAPIKeyMeta(ctx context.Context) ([]map[string]any, error) {
	keys, err := s.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, k := range keys {
		m := map[string]any{
			"name":       k.Name,
			"prefix":     k.Prefix,
			"is_active":  k.IsActive,
			"created_at": k.CreatedAt,
		}
		if k.LastUsedAt != nil {
			m["last_used_at"] = *k.LastUsedAt
		} else {
			m["last_used_at"] = nil
		}
		if k.ExpiresAt != nil {
			m["expires_at"] = *k.ExpiresAt
		} else {
			m["expires_at"] = nil
		}
		out = append(out, m)
	}
	return out, nil
}

// ListAPIKeys returns non-secret metadata for all keys, newest first.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKeyInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, prefix, is_active, created_at, last_used_at, expires_at
		 FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIKeyInfo
	for rows.Next() {
		var k APIKeyInfo
		if err := rows.Scan(&k.Name, &k.Prefix, &k.IsActive, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
