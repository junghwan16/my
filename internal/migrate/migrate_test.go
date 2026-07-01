package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/junghwan16/gieok/internal/storage"
)

func TestApplyCreatesSchemaAndRecordsVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}

	if !hasTable(ctx, t, db, "sources") {
		t.Fatal("sources table missing after Apply")
	}
	if !hasTable(ctx, t, db, "memory_links") {
		t.Fatal("memory_links table missing after Apply")
	}
	if !hasTable(ctx, t, db, "memory_relations") {
		t.Fatal("memory_relations table missing after Apply")
	}
	if !hasColumn(ctx, t, db, "sources", "imported_at") {
		t.Fatal("sources.imported_at column missing after Apply")
	}
	if got, want := dbVersion(ctx, t, db), schemaVersion; got != want {
		t.Fatalf("db version = %d, want %d (fully migrated)", got, want)
	}
	// A fresh database (started at version 0) is not backed up.
	if _, err := os.Stat(path + ".bak-v0"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup for fresh database: err = %v", err)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	// A second call has no migrations left and must return cleanly.
	if err := Apply(ctx, db, path); err != nil {
		t.Fatalf("second Apply returned error: %v", err)
	}
	if got, want := dbVersion(ctx, t, db), schemaVersion; got != want {
		t.Fatalf("db version = %d, want %d (fully migrated)", got, want)
	}
}

func TestApplyRecordsVersionWhenSchemaIsCurrentButLedgerMissing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	if err := applySchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got := dbVersion(ctx, t, db); got != 0 {
		t.Fatalf("db version before Apply = %d, want 0 (schema exists but ledger row missing)", got)
	}

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}

	if got, want := dbVersion(ctx, t, db), schemaVersion; got != want {
		t.Fatalf("db version = %d, want %d (ledger recorded without schema rewrite)", got, want)
	}
	if _, err := os.Stat(path + ".bak-v0"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup for current schema with missing ledger: err = %v", err)
	}
}

func TestApplyBacksUpAlreadyVersionedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	createVersionedLegacySchema(ctx, t, db, 1)
	if got := dbVersion(ctx, t, db); got != 1 {
		t.Fatalf("db version before Apply = %d, want 1", got)
	}

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".bak-v1"); err != nil {
		t.Fatalf("expected backup at %s.bak-v1: %v", path, err)
	}
	if got, want := dbVersion(ctx, t, db), schemaVersion; got != want {
		t.Fatalf("db version = %d, want %d (fully migrated)", got, want)
	}
}

// TestApplyUpgradesLegacyStorePreservingData is the regression guard for the
// migration rewrite: a legacy store at an older version, with populated
// source_events and memory_links, must gain the new memory_relations table
// without rewriting the existing tables. The previous ORM auto-migration rebuilt
// those tables through a __temp copy and blew up a NOT NULL constraint
// (source_events.event_index, memory_links.memory_id), losing data. Explicit
// CREATE TABLE IF NOT EXISTS must leave the rows exactly as they were.
func TestApplyUpgradesLegacyStorePreservingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	createPopulatedLegacyStore(ctx, t, db, 5)

	if hasTable(ctx, t, db, "memory_relations") {
		t.Fatal("legacy store should not have memory_relations before Apply")
	}

	if err := Apply(ctx, db, path); err != nil {
		t.Fatalf("Apply on legacy store: %v", err)
	}

	if !hasTable(ctx, t, db, "memory_relations") {
		t.Fatal("memory_relations table missing after upgrade")
	}
	if got, want := dbVersion(ctx, t, db), schemaVersion; got != want {
		t.Fatalf("db version = %d, want %d after upgrade", got, want)
	}

	// The populated legacy tables must be untouched — same rows, same values.
	if got := rowCount(ctx, t, db, "source_events"); got != 1 {
		t.Fatalf("source_events rows = %d, want 1 preserved through upgrade", got)
	}
	if got := rowCount(ctx, t, db, "memory_links"); got != 1 {
		t.Fatalf("memory_links rows = %d, want 1 preserved through upgrade", got)
	}
	var eventIndex int
	var eventText string
	if err := db.QueryRowContext(ctx,
		"SELECT event_index, text FROM source_events WHERE source_id = 'src:1'",
	).Scan(&eventIndex, &eventText); err != nil {
		t.Fatalf("read preserved source_event: %v", err)
	}
	if eventIndex != 0 || eventText != "hello" {
		t.Fatalf("source_event = (%d, %q), want (0, \"hello\") preserved", eventIndex, eventText)
	}
	var linkMemoryID string
	if err := db.QueryRowContext(ctx,
		"SELECT memory_id FROM memory_links WHERE source_id = 'src:1'",
	).Scan(&linkMemoryID); err != nil {
		t.Fatalf("read preserved memory_link: %v", err)
	}
	if linkMemoryID != "mem:1" {
		t.Fatalf("memory_link memory_id = %q, want mem:1 preserved", linkMemoryID)
	}
}

// createPopulatedLegacyStore builds the pre-memory_relations schema (matching the
// shipped goose-era tables) at the given version and seeds one source, event,
// memory, and link, so a test can assert the upgrade preserves them.
func createPopulatedLegacyStore(ctx context.Context, t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE sources (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, uri TEXT NOT NULL,
			content_sha256 TEXT NOT NULL, scope_kind TEXT NOT NULL, scope_value TEXT NOT NULL,
			started_at TEXT, ended_at TEXT, imported_at TEXT NOT NULL, metadata_json TEXT NOT NULL)`,
		`CREATE TABLE source_events (
			source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			event_index INTEGER NOT NULL, line INTEGER NOT NULL, at TEXT, type TEXT NOT NULL,
			turn_id TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL, raw_json TEXT NOT NULL,
			PRIMARY KEY (source_id, event_index))`,
		`CREATE TABLE memories (
			id TEXT PRIMARY KEY, agent TEXT NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL,
			created_at TEXT NOT NULL, metadata_json TEXT NOT NULL)`,
		`CREATE TABLE memory_links (
			source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			kind TEXT NOT NULL, created_at TEXT NOT NULL, metadata_json TEXT NOT NULL,
			PRIMARY KEY (source_id, memory_id, kind))`,
		`CREATE TABLE memory_vectors (
			memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
			model TEXT NOT NULL, dim INTEGER NOT NULL, vector BLOB NOT NULL)`,
		`CREATE TABLE schema_versions (name TEXT PRIMARY KEY, version INTEGER NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO sources VALUES ('src:1','codex','uri','sha','workspace','/w',NULL,NULL,'2026-07-01T00:00:00Z','{}')`,
		`INSERT INTO source_events (source_id,event_index,line,type,payload_json,raw_json,text) VALUES ('src:1',0,1,'msg','{}','{}','hello')`,
		`INSERT INTO memories VALUES ('mem:1','claude','summary','a memory','2026-07-01T00:00:00Z','{}')`,
		`INSERT INTO memory_links VALUES ('src:1','mem:1','source_ingest','2026-07-01T00:00:00Z','{}')`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed legacy store: %v\nstmt: %s", err, stmt)
		}
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO schema_versions (name, version, updated_at) VALUES (?, ?, '2026-07-01T00:00:00Z')",
		schemaName, version,
	); err != nil {
		t.Fatalf("seed legacy version: %v", err)
	}
}

func rowCount(ctx context.Context, t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func openDB(t *testing.T, path string) (*sql.DB, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	return db, func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func dbVersion(ctx context.Context, t *testing.T, db *sql.DB) int64 {
	t.Helper()
	version, err := currentVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func hasTable(ctx context.Context, t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func hasColumn(ctx context.Context, t *testing.T, db *sql.DB, table string, column string) bool {
	t.Helper()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Fatal(err)
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
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func createVersionedLegacySchema(ctx context.Context, t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `CREATE TABLE sources (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		uri TEXT NOT NULL,
		content_sha256 TEXT NOT NULL,
		scope_kind TEXT NOT NULL,
		scope_value TEXT NOT NULL,
		started_at TEXT,
		ended_at TEXT,
		recorded_at TEXT NOT NULL,
		metadata_json TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_versions (
		name TEXT PRIMARY KEY,
		version INTEGER NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO schema_versions (name, version, updated_at) VALUES (?, ?, '2026-07-01T00:00:00Z')",
		schemaName,
		version,
	); err != nil {
		t.Fatal(err)
	}
}
