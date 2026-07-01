package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/storage"
)

func TestApplyCreatesSchemaAndRecordsVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}

	if !tableExists(ctx, t, db, "sources") {
		t.Fatal("sources table missing after Apply")
	}
	if !tableExists(ctx, t, db, "memory_links") {
		t.Fatal("memory_links table missing after Apply")
	}
	if got, want := dbVersion(ctx, t, db), targetVersion(ctx, t, db); got != want {
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
	// A second call has nothing pending and must be a clean no-op.
	if err := Apply(ctx, db, path); err != nil {
		t.Fatalf("second Apply returned error: %v", err)
	}
	if got, want := dbVersion(ctx, t, db), targetVersion(ctx, t, db); got != want {
		t.Fatalf("db version = %d, want %d (fully migrated)", got, want)
	}
}

func TestApplyBacksUpAlreadyVersionedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	db, closeDB := openDB(t, path)
	defer closeDB()

	// Advance only to v1, leaving v2 pending, so the next Apply runs against an
	// already-versioned database and snapshots it to <db>.bak-v1 first.
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatal(err)
	}
	if got := dbVersion(ctx, t, db); got != 1 {
		t.Fatalf("db version after UpByOne = %d, want 1", got)
	}

	if err := Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".bak-v1"); err != nil {
		t.Fatalf("expected backup at %s.bak-v1: %v", path, err)
	}
	if got, want := dbVersion(ctx, t, db), targetVersion(ctx, t, db); got != want {
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
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// targetVersion is the highest embedded migration version, so tests assert
// "fully migrated" without hardcoding a count that every new migration breaks.
func targetVersion(ctx context.Context, t *testing.T, db *bun.DB) int64 {
	t.Helper()
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	_, target, err := provider.GetVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func tableExists(ctx context.Context, t *testing.T, db *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}
