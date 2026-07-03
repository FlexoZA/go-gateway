package postgres

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// backup.go dumps and restores the gateway's own state. The gateway DB holds NO
// telemetry (that goes to the webhook) — only operational state: the device
// registry, users, API keys, event mappings, server settings, webhooks, and
// clip/snapshot metadata. It is small and, crucially, its schema is owned by this
// application (recreated idempotently on boot), so a logical dump of the table rows
// is a complete, restorable backup without needing pg_dump in the (distroless) image.
//
// A backup is a single .tar.gz containing a manifest.json plus one file per table in
// Postgres's native COPY text format — which round-trips every column type (jsonb,
// bytea, timestamptz) exactly. Restore truncates each table and COPYs the rows back,
// then resets the id sequences.

// backupTables are the durable tables included in a backup, in restore order. The
// high-churn log tables (gateway_errors, device_errors) and the transient
// webhook_outbox delivery queue are deliberately excluded — they are not state worth
// preserving and would bloat the backup.
var backupTables = []string{
	"devices",
	"unknown_devices",
	"event_mappings",
	"users",
	"api_keys",
	"standard_event_codes",
	"server_settings",
	"webhooks",
	"unit_settings",
	"clips",
	"snapshots",
}

// backupManifest is the JSON header describing a backup archive.
type backupManifest struct {
	Version   int              `json:"version"`
	CreatedAt string           `json:"created_at"`
	Tables    []backupTableRef `json:"tables"`
}

type backupTableRef struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
}

const backupVersion = 1

// tableColumns returns a table's column names in ordinal order.
func (s *Store) tableColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// DumpTo writes a gzip-compressed tar backup of the gateway tables to w and returns
// the total row count. createdAt stamps the manifest (the caller supplies it so the
// filename and manifest agree).
func (s *Store) DumpTo(ctx context.Context, w io.Writer, createdAt time.Time) (int64, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("backup acquire: %w", err)
	}
	defer conn.Release()
	pg := conn.Conn().PgConn()

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	manifest := backupManifest{Version: backupVersion, CreatedAt: createdAt.UTC().Format(time.RFC3339)}
	var totalRows int64
	tableData := make(map[string][]byte, len(backupTables))

	for _, table := range backupTables {
		cols, err := s.tableColumns(ctx, table)
		if err != nil {
			return 0, fmt.Errorf("backup columns %s: %w", table, err)
		}
		if len(cols) == 0 {
			continue // table absent (older/newer schema) — skip rather than fail
		}
		var buf bytes.Buffer
		tag, err := pg.CopyTo(ctx, &buf, fmt.Sprintf("COPY %s (%s) TO STDOUT", table, quoteCols(cols)))
		if err != nil {
			return 0, fmt.Errorf("backup copy-out %s: %w", table, err)
		}
		tableData[table] = buf.Bytes()
		manifest.Tables = append(manifest.Tables, backupTableRef{Name: table, Columns: cols, Rows: tag.RowsAffected()})
		totalRows += tag.RowsAffected()
	}

	// manifest.json first so a reader learns the layout before the data.
	mj, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := writeTarFile(tw, "manifest.json", mj); err != nil {
		return 0, err
	}
	for _, t := range manifest.Tables {
		if err := writeTarFile(tw, "tables/"+t.Name+".copy", tableData[t.Name]); err != nil {
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	return totalRows, nil
}

// RestoreFrom replaces the gateway tables with the contents of a .tar.gz produced by
// DumpTo. Each table is truncated and reloaded, and id sequences are reset. This is
// destructive and meant to run against a freshly-booted (schema-created) database.
func (s *Store) RestoreFrom(ctx context.Context, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("restore gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var manifest backupManifest
	data := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("restore read: %w", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		switch {
		case hdr.Name == "manifest.json":
			if err := json.Unmarshal(body, &manifest); err != nil {
				return fmt.Errorf("restore manifest: %w", err)
			}
		case len(hdr.Name) > 7 && hdr.Name[:7] == "tables/":
			data[hdr.Name[7:]] = body
		}
	}
	if manifest.Version != backupVersion {
		return fmt.Errorf("restore: unsupported backup version %d (want %d)", manifest.Version, backupVersion)
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("restore acquire: %w", err)
	}
	defer conn.Release()
	pg := conn.Conn().PgConn()

	for _, t := range manifest.Tables {
		body, ok := data[t.Name+".copy"]
		if !ok {
			return fmt.Errorf("restore: table %s missing from archive", t.Name)
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("TRUNCATE %s", t.Name)); err != nil {
			return fmt.Errorf("restore truncate %s: %w", t.Name, err)
		}
		sql := fmt.Sprintf("COPY %s (%s) FROM STDIN", t.Name, quoteCols(t.Columns))
		if _, err := pg.CopyFrom(ctx, bytes.NewReader(body), sql); err != nil {
			return fmt.Errorf("restore copy-in %s: %w", t.Name, err)
		}
		// Re-align the id sequence so future inserts don't collide with restored ids.
		// Only tables with a serial id column have one (others use a TEXT/composite pk).
		if hasColumn(t.Columns, "id") {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				`SELECT setval(seq, GREATEST((SELECT COALESCE(max(id), 1) FROM %s), 1))
				   FROM pg_get_serial_sequence('%s', 'id') AS seq
				  WHERE seq IS NOT NULL`, t.Name, t.Name)); err != nil {
				return fmt.Errorf("restore reset sequence %s: %w", t.Name, err)
			}
		}
	}
	return nil
}

func hasColumn(cols []string, name string) bool {
	for _, c := range cols {
		if c == name {
			return true
		}
	}
	return false
}

// quoteCols renders a column list for a COPY statement, double-quoting each name.
func quoteCols(cols []string) string {
	var b bytes.Buffer
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(c)
		b.WriteByte('"')
	}
	return b.String()
}

func writeTarFile(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}
