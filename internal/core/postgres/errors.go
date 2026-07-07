package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// errorSinkBuffer bounds how many error lines may queue for the DB writer before
// new ones are dropped. Dropping (rather than blocking) keeps logging off the
// critical path even if Postgres is slow or down — losing some error rows is far
// better than stalling the device read loops.
const errorSinkBuffer = 256

// RecordGatewayError persists one system/server/gateway error line.
func (s *Store) RecordGatewayError(ctx context.Context, unit, namespace, event, message string, fields map[string]any) error {
	var raw []byte
	if len(fields) > 0 {
		raw, _ = json.Marshal(fields)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO gateway_errors (unit, namespace, event, message, fields)
		 VALUES ($1, $2, $3, $4, $5)`,
		nullIfEmpty(unit), nullIfEmpty(namespace), nullIfEmpty(event), message, raw)
	return err
}

// RecordDeviceError persists one error a device reported over its connection. The
// signature is primitives only (no shared struct) so callers — e.g. the gateway
// core — can depend on a small interface without importing this package.
func (s *Store) RecordDeviceError(ctx context.Context, serial, category, message, remoteAddr string, remotePort int, raw []byte) error {
	var payload any
	if len(raw) > 0 {
		// Store as JSONB when the payload is valid JSON; otherwise wrap the raw text.
		if json.Valid(raw) {
			payload = json.RawMessage(raw)
		} else {
			payload, _ = json.Marshal(map[string]string{"raw": string(raw)})
		}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO device_errors (serial, error_category, error_message, remote_address, remote_port, raw_payload)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		serial, nullIfEmpty(category), message, nullIfEmpty(remoteAddr), nullIfZero(remotePort), payload)
	return err
}

// DeleteGatewayErrorsOlderThan deletes up to limit gateway_errors rows created
// before cutoff and returns how many were removed. Batched (via limit) so a large
// backlog is reaped in chunks rather than one giant statement.
func (s *Store) DeleteGatewayErrorsOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM gateway_errors WHERE id IN (
		    SELECT id FROM gateway_errors WHERE created_at < $1 ORDER BY id LIMIT $2
		 )`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete old gateway errors: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteDeviceErrorsOlderThan deletes up to limit device_errors rows created before
// cutoff and returns how many were removed. Batched like the gateway variant.
func (s *Store) DeleteDeviceErrorsOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM device_errors WHERE id IN (
		    SELECT id FROM device_errors WHERE created_at < $1 ORDER BY id LIMIT $2
		 )`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete old device errors: %w", err)
	}
	return tag.RowsAffected(), nil
}

// StartErrorLogSink returns a logging.ErrorSink that persists every Error-level
// line to gateway_errors. Writes happen on a single background goroutine fed by a
// bounded channel, so the logging hot path never blocks on the database; when the
// buffer is full, lines are dropped. The goroutine stops when ctx is cancelled.
//
// log is used only to report the sink's OWN failures (at Debug level, which does
// not re-enter the error sink — avoiding infinite recursion).
func (s *Store) StartErrorLogSink(ctx context.Context, unit string, log *logging.Logger) logging.ErrorSink {
	type entry struct {
		namespace string
		fields    map[string]any
	}
	ch := make(chan entry, errorSinkBuffer)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-ch:
				event, _ := e.fields["event"].(string)
				wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := s.RecordGatewayError(wctx, unit, e.namespace, event, errorMessage(e.fields), e.fields)
				cancel()
				if err != nil {
					log.Debug(map[string]any{"event": "gateway_error_persist_failed", "error": err.Error()})
				}
			}
		}
	}()

	return func(namespace string, fields map[string]any) {
		select {
		case ch <- entry{namespace: namespace, fields: fields}:
		default:
			// Buffer full — drop rather than block the logger.
		}
	}
}

// errorMessage derives a short message column from a log line's fields, preferring
// an explicit error string, then a detail, then the event name.
func errorMessage(fields map[string]any) string {
	for _, k := range []string{"error", "detail", "event"} {
		if v, ok := fields[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func nullIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
