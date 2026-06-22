package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor for password hashing. 12 is a sound default
// (raise it as hardware improves).
const bcryptCost = 12

// minPasswordLength is the minimum accepted password length.
const minPasswordLength = 8

// timingEqualizerHash is a fixed valid bcrypt hash compared against when an email
// is not found, so a missing user costs the same time as a wrong password and
// cannot be distinguished by response timing (mitigates user enumeration).
const timingEqualizerHash = "$2a$12$Dds/Bfw1pF4gZZexdMjFQO8C/fpg1UfbUwNBQ0rD9WJJl57iSCPT."

// ErrWeakPassword is returned when a password is too short.
var ErrWeakPassword = errors.New("password must be at least 8 characters")

// HashPassword returns a salted bcrypt hash (PHC string) for the password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UpsertUser creates a user or resets an existing user's password. The plaintext
// is hashed in-process and only the hash is written (via a parameterized query).
func (s *Store) UpsertUser(ctx context.Context, email, password string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("email required")
	}
	if len(password) < minPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 ON CONFLICT (lower(email)) DO UPDATE
		   SET password_hash = EXCLUDED.password_hash, updated_at = now()`,
		email, hash)
	return err
}

// CountUsers returns how many accounts exist (used to decide first-run setup).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// ListUsers returns all accounts (never the password hash), oldest first.
func (s *Store) ListUsers(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, is_active, created_at, last_login_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var email string
		var active bool
		var createdAt, lastLogin any
		if err := rows.Scan(&id, &email, &active, &createdAt, &lastLogin); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "email": email, "is_active": active,
			"created_at": createdAt, "last_login_at": lastLogin,
		})
	}
	return out, rows.Err()
}

// CreateUser inserts a new account. Unlike UpsertUser it fails (rather than
// resetting the password) when the email already exists, so a "create" in the UI
// can't silently overwrite an existing user.
func (s *Store) CreateUser(ctx context.Context, email, password string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("email required")
	}
	if len(password) < minPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 ON CONFLICT (lower(email)) DO NOTHING`, email, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("a user with that email already exists")
	}
	return nil
}

// SetUserPassword resets an account's password by id.
func (s *Store) SetUserPassword(ctx context.Context, id int64, password string) error {
	if len(password) < minPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserActive enables or disables an account. Disabling is refused when it
// would leave no active users (lock-out guard).
func (s *Store) SetUserActive(ctx context.Context, id int64, active bool) error {
	if !active {
		last, err := s.wouldRemoveLastActiveUser(ctx, id)
		if err != nil {
			return err
		}
		if last {
			return errors.New("cannot disable the last active user")
		}
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET is_active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes an account, refusing to remove the last active user.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	last, err := s.wouldRemoveLastActiveUser(ctx, id)
	if err != nil {
		return err
	}
	if last {
		return errors.New("cannot delete the last active user")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// wouldRemoveLastActiveUser reports whether the given user is active and is the
// only active account — i.e. removing/disabling it would lock everyone out.
func (s *Store) wouldRemoveLastActiveUser(ctx context.Context, id int64) (bool, error) {
	var targetActive bool
	err := s.pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1`, id).Scan(&targetActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // not found; the caller's Exec will report it
		}
		return false, err
	}
	if !targetActive {
		return false, nil
	}
	var othersActive int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE is_active AND id <> $1`, id).Scan(&othersActive); err != nil {
		return false, err
	}
	return othersActive == 0, nil
}

// VerifyUser reports whether the email/password pair is valid and the account is
// active. On success it records last_login_at. It runs in constant-ish time
// whether or not the email exists.
func (s *Store) VerifyUser(ctx context.Context, email, password string) (bool, error) {
	email = strings.TrimSpace(email)
	var hash string
	var active bool
	err := s.pool.QueryRow(ctx,
		`SELECT password_hash, is_active FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&hash, &active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Equalize timing against the wrong-password path.
			_ = bcrypt.CompareHashAndPassword([]byte(timingEqualizerHash), []byte(password))
			return false, nil
		}
		return false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false, nil
	}
	if !active {
		return false, nil
	}
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE lower(email) = lower($1)`, email)
	return true, nil
}
