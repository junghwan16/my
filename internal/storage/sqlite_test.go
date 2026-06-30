package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/junghwan16/my/internal/storage"
)

func TestOpenSQLiteEnablesForeignKeys(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()

	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys pragma = %d, want 1", foreignKeys)
	}
}

func TestOpenSQLiteEnforcesForeignKeyConstraints(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE parent (id TEXT PRIMARY KEY);
		CREATE TABLE child (parent_id TEXT REFERENCES parent(id));
	`); err != nil {
		t.Fatal(err)
	}

	// Inserting a child that references a missing parent must be rejected,
	// proving the REFERENCES constraint is actually enforced.
	if _, err := db.ExecContext(ctx, `INSERT INTO child (parent_id) VALUES ('missing')`); err == nil {
		t.Fatal("expected foreign key violation, got nil error")
	}
}
