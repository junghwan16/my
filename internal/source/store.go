package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/jsonutil"
)

// The store schema and row-model mappings live in schema.go.

// Store records imported sources and their normalized events.
type Store struct {
	db *bun.DB
}

var _ Recorder = (*Store)(nil)

// NewStore returns a source store backed by an already-open database.
func NewStore(db *bun.DB) *Store {
	return &Store{db: db}
}

// RecordSource replaces a source and all of its events atomically.
func (s *Store) RecordSource(ctx context.Context, source Source, events []SourceEvent) error {
	if len(source.MetadataJSON) == 0 {
		source.MetadataJSON = jsonutil.EmptyObject()
	}

	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().
			Model((*sourceEventRow)(nil)).
			Where("source_id = ?", source.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("delete source events: %w", err)
		}

		row := newSourceRow(source)
		if _, err := tx.NewInsert().
			Model(row).
			On("CONFLICT (id) DO UPDATE").
			Set("kind = EXCLUDED.kind").
			Set("uri = EXCLUDED.uri").
			Set("content_sha256 = EXCLUDED.content_sha256").
			Set("scope_kind = EXCLUDED.scope_kind").
			Set("scope_value = EXCLUDED.scope_value").
			Set("started_at = EXCLUDED.started_at").
			Set("ended_at = EXCLUDED.ended_at").
			Set("recorded_at = EXCLUDED.recorded_at").
			Set("metadata_json = EXCLUDED.metadata_json").
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert source: %w", err)
		}

		rows := make([]sourceEventRow, 0, len(events))
		for _, event := range events {
			if event.SourceID == "" {
				event.SourceID = source.ID
			}
			rows = append(rows, newSourceEventRow(event))
		}
		if len(rows) == 0 {
			return nil
		}
		if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
			return fmt.Errorf("insert source events: %w", err)
		}
		return nil
	})
}

// Source loads one source by ID.
func (s *Store) Source(ctx context.Context, id SourceID) (Source, error) {
	row := new(sourceRow)
	if err := s.db.NewSelect().
		Model(row).
		Where("id = ?", id).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Source{}, fmt.Errorf("source %q not found", id)
		}
		return Source{}, fmt.Errorf("load source: %w", err)
	}
	return row.toSource(), nil
}

// Sources lists all recorded sources in stable import order.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	var rows []sourceRow
	if err := s.db.NewSelect().
		Model(&rows).
		Order("recorded_at", "id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}

	sources := make([]Source, 0, len(rows))
	for _, row := range rows {
		sources = append(sources, row.toSource())
	}
	return sources, nil
}

// SourceEvents lists normalized events for a source in their original order.
func (s *Store) SourceEvents(ctx context.Context, id SourceID) ([]SourceEvent, error) {
	var rows []sourceEventRow
	if err := s.db.NewSelect().
		Model(&rows).
		Where("source_id = ?", id).
		Order("event_index").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source events: %w", err)
	}

	events := make([]SourceEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, row.toSourceEvent())
	}
	return events, nil
}
