// Package migrate owns the schema history for the local SQLite store. The DDL
// lives as goose migration files under migrations/, embedded into the binary so
// the single-file desktop build carries its own schema. The domain packages
// (source, memory) keep only row models and queries.
package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Apply brings db up to the latest schema version using goose. Before touching
// an already-versioned database it snapshots the file, so this irreplaceable
// local memory store can be restored if a migration fails.
func Apply(ctx context.Context, db *bun.DB, dbPath string) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}

	current, target, err := provider.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("read migration versions: %w", err)
	}
	if current >= target {
		return nil
	}

	// Back up only an already-versioned database: version 0 is either a brand-new
	// database or one predating migrations, whose baseline step is a no-op
	// CREATE IF NOT EXISTS with nothing worth protecting.
	if current > 0 {
		if err := backup(ctx, db, dbPath, current); err != nil {
			return err
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func newProvider(db *bun.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.DB, sub)
	if err != nil {
		return nil, fmt.Errorf("build migration provider: %w", err)
	}
	return provider, nil
}

func backup(ctx context.Context, db *bun.DB, dbPath string, fromVersion int64) error {
	backupPath := fmt.Sprintf("%s.bak-v%d", dbPath, fromVersion)
	// VACUUM INTO writes a consistent snapshot even under WAL, unlike a raw file
	// copy that could miss the -wal contents. It cannot run inside a transaction.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", backupPath); err != nil {
		return fmt.Errorf("back up database before migration: %w", err)
	}
	return nil
}
