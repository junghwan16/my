package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"

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

func openDB(t *testing.T, path string) (*bun.DB, func()) {
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

func dbVersion(ctx context.Context, t *testing.T, db *bun.DB) int64 {
	t.Helper()
	gormDB, err := openGORM(db)
	if err != nil {
		t.Fatal(err)
	}
	version, err := currentVersion(ctx, gormDB, db)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func hasTable(ctx context.Context, t *testing.T, db *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func hasColumn(ctx context.Context, t *testing.T, db *bun.DB, table string, column string) bool {
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

func createVersionedLegacySchema(ctx context.Context, t *testing.T, db *bun.DB, version int64) {
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
