// Package migrate owns the schema for the local SQLite store. It applies the
// schema with explicit, idempotent DDL (CREATE TABLE IF NOT EXISTS) rather than
// an ORM auto-migration: GORM's SQLite auto-migrate rebuilt an existing table
// through a __temp copy whenever it thought a column or constraint differed, and
// that copy could misalign columns and blow up a NOT NULL constraint on a legacy
// store (e.g. source_events.event_index, memory_links.memory_id). CREATE TABLE
// IF NOT EXISTS never rewrites a populated table, so an upgrade only adds what is
// missing and leaves existing rows untouched. The domain packages (source,
// memory) keep only row models and queries.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	schemaName    = "gieok"
	schemaVersion = int64(6)
)

// tableDef is one managed table: the idempotent DDL that creates it and its
// indexes, and the columns the shape check expects to find. The DDL is the
// single source of truth for the schema, and columns is derived from the same
// definition so the shape check cannot silently drift from what applySchema
// creates.
type tableDef struct {
	name    string
	columns []string
	ddl     []string // executed in order; every statement is idempotent
}

// managedTables lists every regular table in foreign-key dependency order, so a
// referenced table (sources, memories) is always created before the table that
// references it (source_events, memory_links, memory_relations, memory_vectors).
// The full-text index memories_fts is a virtual table created separately (see
// createMemoryFTS).
var managedTables = []tableDef{
	{
		name:    "schema_versions",
		columns: []string{"name", "version", "updated_at"},
		ddl: []string{`CREATE TABLE IF NOT EXISTS schema_versions (
	name TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	updated_at TEXT NOT NULL
)`},
	},
	{
		name:    "sources",
		columns: []string{"id", "kind", "uri", "content_sha256", "scope_kind", "scope_value", "started_at", "ended_at", "imported_at", "metadata_json"},
		ddl: []string{
			`CREATE TABLE IF NOT EXISTS sources (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	uri TEXT NOT NULL,
	content_sha256 TEXT NOT NULL,
	scope_kind TEXT NOT NULL,
	scope_value TEXT NOT NULL,
	started_at TEXT,
	ended_at TEXT,
	imported_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL
)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS sources_kind_hash_idx ON sources (kind, content_sha256)`,
		},
	},
	{
		name:    "source_events",
		columns: []string{"source_id", "event_index", "line", "at", "type", "turn_id", "role", "text", "payload_json", "raw_json"},
		ddl: []string{`CREATE TABLE IF NOT EXISTS source_events (
	source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	event_index INTEGER NOT NULL,
	line INTEGER NOT NULL,
	at TEXT,
	type TEXT NOT NULL,
	turn_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL,
	raw_json TEXT NOT NULL,
	PRIMARY KEY (source_id, event_index)
)`},
	},
	{
		name:    "memories",
		columns: []string{"id", "agent", "kind", "text", "created_at", "metadata_json"},
		ddl: []string{
			`CREATE TABLE IF NOT EXISTS memories (
	id TEXT PRIMARY KEY,
	agent TEXT NOT NULL,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL
)`,
			`CREATE INDEX IF NOT EXISTS memories_agent_idx ON memories (agent)`,
		},
	},
	{
		name:    "memory_links",
		columns: []string{"source_id", "memory_id", "kind", "created_at", "metadata_json"},
		ddl: []string{`CREATE TABLE IF NOT EXISTS memory_links (
	source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	PRIMARY KEY (source_id, memory_id, kind)
)`},
	},
	{
		name:    "memory_relations",
		columns: []string{"from_memory_id", "to_memory_id", "kind", "created_at", "metadata_json"},
		ddl: []string{
			`CREATE TABLE IF NOT EXISTS memory_relations (
	from_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	to_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	PRIMARY KEY (from_memory_id, to_memory_id, kind)
)`,
			`CREATE INDEX IF NOT EXISTS memory_relations_to_idx ON memory_relations (to_memory_id)`,
		},
	},
	{
		name:    "memory_vectors",
		columns: []string{"memory_id", "model", "dim", "vector"},
		ddl: []string{`CREATE TABLE IF NOT EXISTS memory_vectors (
	memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
	model TEXT NOT NULL,
	dim INTEGER NOT NULL,
	vector BLOB NOT NULL
)`},
	},
}

// Apply brings db up to the latest schema version. Before changing an existing
// local memory store it snapshots the file, so user memory can be restored if a
// migration fails.
func Apply(ctx context.Context, db *sql.DB, dbPath string) error {
	current, err := currentVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	shapeCurrent, err := schemaShapeCurrent(ctx, db)
	if err != nil {
		return err
	}
	if shapeCurrent {
		// The physical schema already matches the latest definition; only the
		// version ledger is behind (a legacy store, or a prior run that applied
		// the schema but failed to record the version). Stamp the version without
		// the costly backup + DDL pass.
		if current < schemaVersion {
			return recordVersion(ctx, db)
		}
		return nil
	}

	existing, err := hasExistingStore(ctx, db)
	if err != nil {
		return err
	}
	if existing {
		if err := backup(ctx, db, dbPath, current); err != nil {
			return err
		}
	}
	if err := applySchema(ctx, db); err != nil {
		return err
	}
	return recordVersion(ctx, db)
}

// currentVersion reads the recorded schema version from the schema_versions
// ledger, falling back to the legacy goose ledger, and 0 when neither records a
// version.
func currentVersion(ctx context.Context, db *sql.DB) (int64, error) {
	exists, err := tableExists(ctx, db, "schema_versions")
	if err != nil {
		return 0, err
	}
	if exists {
		var version sql.NullInt64
		err := db.QueryRowContext(ctx,
			"SELECT version FROM schema_versions WHERE name = ?", schemaName,
		).Scan(&version)
		if err == nil {
			if version.Valid {
				return version.Int64, nil
			}
			return 0, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("read schema version row: %w", err)
		}
		// The ledger exists but has no row for this schema yet; fall through to
		// the legacy check.
	}
	return legacyGooseVersion(ctx, db)
}

func legacyGooseVersion(ctx context.Context, db *sql.DB) (int64, error) {
	exists, err := tableExists(ctx, db, "goose_db_version")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}

	var version sql.NullInt64
	if err := db.QueryRowContext(ctx,
		"SELECT max(version_id) FROM goose_db_version WHERE is_applied = 1",
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("read legacy goose version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return version.Int64, nil
}

// schemaShapeCurrent reports whether the physical database already has every
// managed table (plus the FTS index) and every column those tables declare. It
// lets Apply skip the backup and DDL pass when only the version ledger is behind.
func schemaShapeCurrent(ctx context.Context, db *sql.DB) (bool, error) {
	for _, table := range managedTables {
		exists, err := tableExists(ctx, db, table.name)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
		for _, column := range table.columns {
			has, err := columnExists(ctx, db, table.name, column)
			if err != nil {
				return false, err
			}
			if !has {
				return false, nil
			}
		}
	}
	return tableExists(ctx, db, "memories_fts")
}

// hasExistingStore reports whether any known table is already present, so a
// fresh database is not needlessly backed up.
func hasExistingStore(ctx context.Context, db *sql.DB) (bool, error) {
	for _, table := range []string{
		"sources",
		"source_events",
		"memories",
		"memory_links",
		"memory_relations",
		"memories_fts",
		"memory_vectors",
		"schema_versions",
		"goose_db_version",
	} {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// applySchema creates every managed table, index, and the FTS virtual table with
// idempotent DDL. Existing tables are left as they are — only missing objects are
// created — so upgrading a populated store never rewrites or corrupts its data.
func applySchema(ctx context.Context, db *sql.DB) error {
	if err := renameImportedAt(ctx, db); err != nil {
		return err
	}
	for _, table := range managedTables {
		for _, stmt := range table.ddl {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create %s schema: %w", table.name, err)
			}
		}
	}
	return createMemoryFTS(ctx, db)
}

// renameImportedAt migrates the pre-rename sources column recorded_at to
// imported_at. It runs before the CREATE statements so the shape check and
// inserts see the current column name. A plain column rename preserves the data,
// unlike a table rebuild.
func renameImportedAt(ctx context.Context, db *sql.DB) error {
	sources, err := tableExists(ctx, db, "sources")
	if err != nil {
		return err
	}
	if !sources {
		return nil
	}
	hasImported, err := columnExists(ctx, db, "sources", "imported_at")
	if err != nil {
		return err
	}
	hasRecorded, err := columnExists(ctx, db, "sources", "recorded_at")
	if err != nil {
		return err
	}
	if !hasRecorded || hasImported {
		return nil
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE sources RENAME COLUMN recorded_at TO imported_at"); err != nil {
		return fmt.Errorf("rename sources import timestamp column: %w", err)
	}
	return nil
}

func createMemoryFTS(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
	memory_id UNINDEXED,
	tokens,
	tokenize = 'unicode61'
)`)
	if err != nil {
		return fmt.Errorf("create memory full-text index: %w", err)
	}
	return nil
}

// recordVersion stamps the current schema version into the ledger, upserting the
// single row keyed by schemaName.
func recordVersion(ctx context.Context, db *sql.DB) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_versions (name, version, updated_at) VALUES (?, ?, ?)
	ON CONFLICT(name) DO UPDATE SET version = excluded.version, updated_at = excluded.updated_at`,
		schemaName, schemaVersion, now,
	); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

func backup(ctx context.Context, db *sql.DB, dbPath string, fromVersion int64) error {
	backupPath := fmt.Sprintf("%s.bak-v%d", dbPath, fromVersion)
	// VACUUM INTO refuses to overwrite, so a stale backup from a prior aborted
	// run would block every retry. Remove it first so the migration can proceed.
	if err := os.Remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale backup %s: %w", backupPath, err)
	}
	// VACUUM INTO writes a consistent snapshot even under WAL, unlike a plain
	// file copy that could miss the -wal contents. It cannot run inside a transaction.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", backupPath); err != nil {
		return fmt.Errorf("back up database before migration: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?", table,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check table %q: %w", table, err)
	}
	return count > 0, nil
}

func columnExists(ctx context.Context, db *sql.DB, table string, column string) (found bool, err error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("list columns for %s: %w", table, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close columns for %s: %w", table, closeErr)
		}
	}()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan column for %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate columns for %s: %w", table, err)
	}
	return false, nil
}
