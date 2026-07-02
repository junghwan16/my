// Package migrate owns the schema for the local SQLite store. GORM applies the
// regular table schema from Go models, while SQLite-only objects such as FTS5
// are created with explicit SQL. The domain packages (source, memory) keep only
// row models and queries.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/glebarez/sqlite"
)

const (
	schemaName    = "gieok"
	schemaVersion = int64(7)
)

// Apply brings db up to the latest schema version. Before changing an existing
// local memory store it snapshots the file, so user memory can be restored if a
// migration fails.
func Apply(ctx context.Context, db *sql.DB, dbPath string) error {
	gormDB, err := openGORM(db)
	if err != nil {
		return err
	}

	current, err := currentVersion(ctx, gormDB, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	shapeCurrent, err := schemaShapeCurrent(ctx, gormDB)
	if err != nil {
		return err
	}
	if shapeCurrent {
		// The physical schema already matches the latest models; only the
		// version ledger is behind (e.g. a legacy store, or a prior run that
		// applied the schema but failed to record the version). Stamp the
		// version without the costly backup + rewrite.
		if current < schemaVersion {
			return recordVersion(ctx, gormDB)
		}
		return nil
	}

	if hasExistingStore(ctx, gormDB) {
		if err := backup(ctx, db, dbPath, current); err != nil {
			return err
		}
	}
	if err := applySchema(ctx, gormDB, db); err != nil {
		return err
	}
	return recordVersion(ctx, gormDB)
}

func openGORM(db *sql.DB) (*gorm.DB, error) {
	gormDB, err := gorm.Open(sqlite.Dialector{Conn: db}, &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: false,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open gorm migrator: %w", err)
	}
	return gormDB, nil
}

func currentVersion(ctx context.Context, gormDB *gorm.DB, db *sql.DB) (int64, error) {
	if gormDB.WithContext(ctx).Migrator().HasTable(&schemaState{}) {
		var state schemaState
		err := gormDB.WithContext(ctx).First(&state, "name = ?", schemaName).Error
		if err == nil {
			return state.Version, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
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
// table and column the current models declare. The models are the single
// source of truth, so the expected columns are derived from them rather than
// listed by hand — the check cannot drift when a model changes.
func schemaShapeCurrent(ctx context.Context, gormDB *gorm.DB) (bool, error) {
	gormDB = gormDB.WithContext(ctx)
	migrator := gormDB.Migrator()
	for _, model := range []any{
		&schemaState{},
		&sourceModel{},
		&sourceEventModel{},
		&memoryModel{},
		&memoryLinkModel{},
		&memoryRelationModel{},
		&memoryVectorModel{},
	} {
		if !migrator.HasTable(model) {
			return false, nil
		}
		sch, err := schema.Parse(model, &sync.Map{}, gormDB.NamingStrategy)
		if err != nil {
			return false, fmt.Errorf("parse model schema for shape check: %w", err)
		}
		for _, column := range sch.DBNames {
			if !migrator.HasColumn(model, column) {
				return false, nil
			}
		}
	}
	if !migrator.HasTable("memories_fts") {
		return false, nil
	}
	return true, nil
}

func hasExistingStore(ctx context.Context, gormDB *gorm.DB) bool {
	migrator := gormDB.WithContext(ctx).Migrator()
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
		if migrator.HasTable(table) {
			return true
		}
	}
	return false
}

func applySchema(ctx context.Context, gormDB *gorm.DB, db *sql.DB) error {
	if err := renameImportedAt(ctx, db); err != nil {
		return err
	}

	err := gormDB.WithContext(ctx).AutoMigrate(
		&schemaState{},
		&sourceModel{},
		&sourceEventModel{},
		&memoryModel{},
		&memoryLinkModel{},
		&memoryRelationModel{},
		&memoryVectorModel{},
	)
	if err != nil {
		return fmt.Errorf("apply gorm schema: %w", err)
	}
	if err := createMemoryFTS(ctx, db); err != nil {
		return err
	}
	return nil
}

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

func recordVersion(ctx context.Context, gormDB *gorm.DB) error {
	state := schemaState{
		Name:      schemaName,
		Version:   schemaVersion,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := gormDB.WithContext(ctx).Save(&state).Error; err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

func backup(ctx context.Context, db *sql.DB, dbPath string, fromVersion int64) error {
	backupPath := fmt.Sprintf("%s.bak-v%d", dbPath, fromVersion)
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

type schemaState struct {
	Name      string `gorm:"column:name;type:text;primaryKey"`
	Version   int64  `gorm:"column:version;not null"`
	UpdatedAt string `gorm:"column:updated_at;type:text;not null"`
}

func (schemaState) TableName() string {
	return "schema_versions"
}

type sourceModel struct {
	ID            string             `gorm:"column:id;type:text;primaryKey"`
	Kind          string             `gorm:"column:kind;type:text;not null;uniqueIndex:sources_kind_hash_idx,priority:1"`
	URI           string             `gorm:"column:uri;type:text;not null"`
	ContentSHA256 string             `gorm:"column:content_sha256;type:text;not null;uniqueIndex:sources_kind_hash_idx,priority:2"`
	ScopeKind     string             `gorm:"column:scope_kind;type:text;not null"`
	ScopeValue    string             `gorm:"column:scope_value;type:text;not null"`
	StartedAt     time.Time          `gorm:"column:started_at;type:text"`
	EndedAt       time.Time          `gorm:"column:ended_at;type:text"`
	ImportedAt    time.Time          `gorm:"column:imported_at;type:text;not null"`
	MetadataJSON  string             `gorm:"column:metadata_json;type:text;not null"`
	Events        []sourceEventModel `gorm:"foreignKey:SourceID;references:ID;constraint:OnDelete:CASCADE"`
}

func (sourceModel) TableName() string {
	return "sources"
}

type sourceEventModel struct {
	SourceID    string      `gorm:"column:source_id;type:text;primaryKey"`
	Index       int         `gorm:"column:event_index;primaryKey"`
	Line        int         `gorm:"column:line;not null"`
	At          time.Time   `gorm:"column:at;type:text"`
	Type        string      `gorm:"column:type;type:text;not null"`
	TurnID      string      `gorm:"column:turn_id;type:text;not null;default:''"`
	Role        string      `gorm:"column:role;type:text;not null;default:''"`
	Text        string      `gorm:"column:text;type:text;not null;default:''"`
	PayloadJSON string      `gorm:"column:payload_json;type:text;not null"`
	RawJSON     string      `gorm:"column:raw_json;type:text;not null"`
	Source      sourceModel `gorm:"foreignKey:SourceID;references:ID;constraint:OnDelete:CASCADE"`
}

func (sourceEventModel) TableName() string {
	return "source_events"
}

type memoryModel struct {
	ID           string    `gorm:"column:id;type:text;primaryKey"`
	Agent        string    `gorm:"column:agent;type:text;not null;index:memories_agent_idx"`
	Kind         string    `gorm:"column:kind;type:text;not null"`
	Text         string    `gorm:"column:text;type:text;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;type:text;not null"`
	MetadataJSON string    `gorm:"column:metadata_json;type:text;not null"`
	// TextOverride is a nullable human correction layered over Text without
	// changing the Memory's hashed identity or provenance (ADR-0010).
	TextOverride *string           `gorm:"column:text_override;type:text"`
	Links        []memoryLinkModel `gorm:"foreignKey:MemoryID;references:ID;constraint:OnDelete:CASCADE"`
}

func (memoryModel) TableName() string {
	return "memories"
}

type memoryLinkModel struct {
	SourceID     string      `gorm:"column:source_id;type:text;primaryKey"`
	MemoryID     string      `gorm:"column:memory_id;type:text;primaryKey"`
	Kind         string      `gorm:"column:kind;type:text;primaryKey"`
	CreatedAt    time.Time   `gorm:"column:created_at;type:text;not null"`
	MetadataJSON string      `gorm:"column:metadata_json;type:text;not null"`
	Source       sourceModel `gorm:"foreignKey:SourceID;references:ID;constraint:OnDelete:CASCADE"`
	Memory       memoryModel `gorm:"foreignKey:MemoryID;references:ID;constraint:OnDelete:CASCADE"`
}

func (memoryLinkModel) TableName() string {
	return "memory_links"
}

// memoryRelationModel is a directed Memory->Memory relation (issue #16). Unlike a
// memory_link (Source->Memory provenance), a relation connects a new Memory to an
// existing one the agent chose to build on. Both endpoints cascade on delete, so
// deleting either Memory leaves no dangling relation. The kind column is a fixed
// "relates" today but is part of the primary key so future relation types can
// coexist between the same pair without a schema change.
type memoryRelationModel struct {
	FromMemoryID string      `gorm:"column:from_memory_id;type:text;primaryKey"`
	ToMemoryID   string      `gorm:"column:to_memory_id;type:text;primaryKey;index:memory_relations_to_idx"`
	Kind         string      `gorm:"column:kind;type:text;primaryKey"`
	CreatedAt    time.Time   `gorm:"column:created_at;type:text;not null"`
	MetadataJSON string      `gorm:"column:metadata_json;type:text;not null"`
	From         memoryModel `gorm:"foreignKey:FromMemoryID;references:ID;constraint:OnDelete:CASCADE"`
	To           memoryModel `gorm:"foreignKey:ToMemoryID;references:ID;constraint:OnDelete:CASCADE"`
}

func (memoryRelationModel) TableName() string {
	return "memory_relations"
}

type memoryVectorModel struct {
	MemoryID string      `gorm:"column:memory_id;type:text;primaryKey"`
	Model    string      `gorm:"column:model;type:text;not null"`
	Dim      int         `gorm:"column:dim;not null"`
	Vector   []byte      `gorm:"column:vector;type:blob;not null"`
	Memory   memoryModel `gorm:"foreignKey:MemoryID;references:ID;constraint:OnDelete:CASCADE"`
}

func (memoryVectorModel) TableName() string {
	return "memory_vectors"
}
