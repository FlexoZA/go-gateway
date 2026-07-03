package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// clips.go backs recorded-video clips: the metadata for .mp4 files pulled off a
// device's SD card via H-Protocol playback. The bytes live on disk under
// CLIPS_ROOT; this table tracks the request, transfer progress, and result.
//
// The receive side (media.ClipRegistry) drives Create/Status/Progress/Finish as
// frames arrive; the admin API uses List/Get/Delete.

// CreateClip inserts a new clip request (status 'requested') and returns its id.
func (s *Store) CreateClip(ctx context.Context, serial string, camera, profile int, startUtc, endUtc int64, storagePath string) (int64, error) {
	dur := endUtc - startUtc
	if dur < 0 {
		dur = 0
	}
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO clips (serial, camera, profile, start_utc, end_utc, duration_secs, storage_path, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'requested') RETURNING id`,
		serial, camera, profile, startUtc, endUtc, dur, storagePath).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create clip: %w", err)
	}
	return id, nil
}

// UpdateClipStatus sets a clip's status and (optionally) an error message.
func (s *Store) UpdateClipStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE clips SET status = $2, error = $3, updated_at = now() WHERE id = $1`,
		id, status, errMsg)
	if err != nil {
		return fmt.Errorf("update clip status: %w", err)
	}
	return nil
}

// UpdateClipProgress records bytes received so far (and the best-known total),
// flipping status to 'receiving'. Called periodically while frames arrive.
func (s *Store) UpdateClipProgress(ctx context.Context, id, bytesReceived, fileSize int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE clips SET bytes_received = $2, file_size = GREATEST(file_size, $3),
		   status = CASE WHEN status IN ('requested') THEN 'receiving' ELSE status END,
		   updated_at = now()
		 WHERE id = $1`,
		id, bytesReceived, fileSize)
	if err != nil {
		return fmt.Errorf("update clip progress: %w", err)
	}
	return nil
}

// FinishClip marks a clip ready with its final on-disk size.
func (s *Store) FinishClip(ctx context.Context, id, fileSize int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE clips SET status = 'ready', file_size = $2, bytes_received = $2, error = '', updated_at = now()
		 WHERE id = $1`,
		id, fileSize)
	if err != nil {
		return fmt.Errorf("finish clip: %w", err)
	}
	return nil
}

// ListClips returns clips (optionally filtered by serial), newest first.
func (s *Store) ListClips(ctx context.Context, serial string, limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, serial, camera, profile, start_utc, end_utc, duration_secs,
	             status, file_size, bytes_received, storage_path, error, created_at, updated_at
	      FROM clips`
	args := []any{}
	if serial != "" {
		q += ` WHERE serial = $1`
		args = append(args, serial)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list clips: %w", err)
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		m, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetClip returns one clip's metadata by id, or ErrNotFound.
func (s *Store) GetClip(ctx context.Context, id int64) (map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, serial, camera, profile, start_utc, end_utc, duration_secs,
		        status, file_size, bytes_received, storage_path, error, created_at, updated_at
		 FROM clips WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get clip: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanClip(rows)
}

// DeleteClip removes a clip row and returns its storage_path so the caller can
// delete the file from disk.
func (s *Store) DeleteClip(ctx context.Context, id int64) (string, error) {
	var path string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM clips WHERE id = $1 RETURNING storage_path`, id).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete clip: %w", err)
	}
	return path, nil
}

// DeleteClipsOlderThan deletes up to limit clip rows created before cutoff and
// returns their storage paths so the caller can unlink the .mp4 files. Batched (via
// limit) so a large backlog is reaped in chunks rather than one giant statement.
func (s *Store) DeleteClipsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx,
		`DELETE FROM clips WHERE id IN (
		    SELECT id FROM clips WHERE created_at < $1 ORDER BY id LIMIT $2
		 ) RETURNING storage_path`,
		cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("delete old clips: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// scanClip reads a clips row (column order must match the SELECTs above).
func scanClip(rows interface{ Scan(...any) error }) (map[string]any, error) {
	var id, startUtc, endUtc, fileSize, bytesReceived int64
	var camera, profile, durationSecs int
	var serial, status, storagePath, errMsg string
	var createdAt, updatedAt any
	if err := rows.Scan(&id, &serial, &camera, &profile, &startUtc, &endUtc, &durationSecs,
		&status, &fileSize, &bytesReceived, &storagePath, &errMsg, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "serial": serial, "camera": camera, "profile": profile,
		"start_utc": startUtc, "end_utc": endUtc, "duration_secs": durationSecs,
		"status": status, "file_size": fileSize, "bytes_received": bytesReceived,
		"storage_path": storagePath, "error": errMsg,
		"created_at": createdAt, "updated_at": updatedAt,
	}, nil
}
