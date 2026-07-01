package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite" // Register the pure-Go SQLite database/sql driver.
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
)

// sqlitePragmas are applied to every new connection. foreign_keys enforces the
// REFERENCES/ON DELETE CASCADE constraints (off by default in SQLite),
// busy_timeout avoids spurious "database is locked" errors, and WAL improves
// read/write concurrency for the local store.
const sqlitePragmas = "_pragma=busy_timeout(15000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"

// OpenSQLite opens a SQLite database handle with the store's required pragmas.
//
// Schema ownership lives in internal/migrate; this package only owns connection
// and driver concerns.
func OpenSQLite(path string) (*bun.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite parent directory: %w", err)
	}

	db, err := sql.Open("sqlite", fmt.Sprintf("%s?%s", path, sqlitePragmas))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// modernc's SQLite serializes writes per file; a single connection avoids
	// lock contention for this single-process CLI store.
	db.SetMaxOpenConns(1)
	return bun.NewDB(db, sqlitedialect.New()), nil
}
