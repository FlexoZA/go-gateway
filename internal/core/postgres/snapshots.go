package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// snapshots.go backs gateway-saved snapshots: the metadata for JPEG stills stored
// on the server under CLIPS_ROOT/snapshots. The bytes live on disk; this table
// tracks where they came from. The HTTP API drives Create/List/Get/Delete.

// CreateSnapshot inserts a saved-snapshot row and returns its id.
func (s *Store) CreateSnapshot(ctx context.Context, serial string, camera int, kind, source string, capturedUTC int64, devicePath, storagePath string, fileSize int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO snapshots (serial, camera, kind, source, captured_utc, device_path, storage_path, file_size)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		serial, camera, kind, source, capturedUTC, devicePath, storagePath, fileSize).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create snapshot: %w", err)
	}
	return id, nil
}

// ListSnapshots returns saved snapshots (optionally filtered by serial), newest first.
func (s *Store) ListSnapshots(ctx context.Context, serial string, limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, serial, camera, kind, source, captured_utc, device_path, storage_path, file_size, created_at
	      FROM snapshots`
	args := []any{}
	if serial != "" {
		q += ` WHERE serial = $1`
		args = append(args, serial)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetSnapshot returns one saved snapshot's metadata by id, or ErrNotFound.
func (s *Store) GetSnapshot(ctx context.Context, id int64) (map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, serial, camera, kind, source, captured_utc, device_path, storage_path, file_size, created_at
		 FROM snapshots WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanSnapshot(rows)
}

// DeleteSnapshot removes a snapshot row and returns its storage_path so the caller
// can delete the file from disk.
func (s *Store) DeleteSnapshot(ctx context.Context, id int64) (string, error) {
	var path string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM snapshots WHERE id = $1 RETURNING storage_path`, id).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete snapshot: %w", err)
	}
	return path, nil
}

// scanSnapshot reads a snapshots row (column order must match the SELECTs above).
func scanSnapshot(rows interface{ Scan(...any) error }) (map[string]any, error) {
	var id, capturedUTC, fileSize int64
	var camera int
	var serial, kind, source, devicePath, storagePath string
	var createdAt any
	if err := rows.Scan(&id, &serial, &camera, &kind, &source, &capturedUTC, &devicePath, &storagePath, &fileSize, &createdAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "serial": serial, "camera": camera, "kind": kind, "source": source,
		"captured_utc": capturedUTC, "device_path": devicePath, "storage_path": storagePath,
		"file_size": fileSize, "created_at": createdAt,
	}, nil
}
