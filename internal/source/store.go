package sources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/junghwan16/gieok/internal/jsonutil"
)

// The store row-model mappings live in rows.go; the schema lives in the
// migrate package's ledger.

// Store saves imported Sources and their normalized events.
type Store struct {
	db *sql.DB
}

var _ SourceWriter = (*Store)(nil)

// NewStore returns a source store backed by an already-open database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// SaveSource replaces a source and all of its events atomically.
func (s *Store) SaveSource(ctx context.Context, source Source, events []SourceEvent) error {
	if len(source.MetadataJSON) == 0 {
		source.MetadataJSON = jsonutil.EmptyObject()
	}

	return s.runTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM source_events WHERE source_id = ?", source.ID); err != nil {
			return fmt.Errorf("delete source events: %w", err)
		}

		row := newSourceRow(source)
		if _, err := tx.ExecContext(ctx, `INSERT INTO sources (
			id,
			kind,
			uri,
			content_sha256,
			scope_kind,
			scope_value,
			started_at,
			ended_at,
			imported_at,
			metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			uri = EXCLUDED.uri,
			content_sha256 = EXCLUDED.content_sha256,
			scope_kind = EXCLUDED.scope_kind,
			scope_value = EXCLUDED.scope_value,
			started_at = EXCLUDED.started_at,
			ended_at = EXCLUDED.ended_at,
			imported_at = EXCLUDED.imported_at,
			metadata_json = EXCLUDED.metadata_json`,
			row.ID,
			row.Kind,
			row.URI,
			row.ContentSHA256,
			row.ScopeKind,
			row.ScopeValue,
			nullableTime(row.StartedAt),
			nullableTime(row.EndedAt),
			row.ImportedAt,
			row.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert source: %w", err)
		}

		for _, event := range events {
			if event.SourceID == "" {
				event.SourceID = source.ID
			}
			row := newSourceEventRow(event)
			if _, err := tx.ExecContext(ctx, `INSERT INTO source_events (
				source_id,
				event_index,
				line,
				at,
				type,
				turn_id,
				role,
				text,
				payload_json,
				raw_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.SourceID,
				row.Index,
				row.Line,
				nullableTime(row.At),
				row.Type,
				row.TurnID,
				row.Role,
				row.Text,
				row.PayloadJSON,
				row.RawJSON,
			); err != nil {
				return fmt.Errorf("insert source events: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) runTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w; rollback source transaction: %w", err, rollbackErr)
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit source transaction: %w", err)
	}
	return nil
}

// Source loads one source by ID.
func (s *Store) Source(ctx context.Context, id SourceID) (Source, error) {
	row, err := scanSourceRow(s.db.QueryRowContext(ctx, `SELECT
		id,
		kind,
		uri,
		content_sha256,
		scope_kind,
		scope_value,
		started_at,
		ended_at,
		imported_at,
		metadata_json
	FROM sources
	WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Source{}, fmt.Errorf("source %q not found", id)
		}
		return Source{}, fmt.Errorf("load source: %w", err)
	}
	return row.toSource(), nil
}

// Sources lists all saved Sources in stable import order.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id,
		kind,
		uri,
		content_sha256,
		scope_kind,
		scope_value,
		started_at,
		ended_at,
		imported_at,
		metadata_json
	FROM sources
	ORDER BY imported_at, id`)
	if err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}
	defer closeRows(rows)

	var sources []Source
	for rows.Next() {
		row, err := scanSourceRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, row.toSource())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return sources, nil
}

// SourceEvents lists normalized events for a source in their original order.
func (s *Store) SourceEvents(ctx context.Context, id SourceID) ([]SourceEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		source_id,
		event_index,
		line,
		at,
		type,
		turn_id,
		role,
		text,
		payload_json,
		raw_json
	FROM source_events
	WHERE source_id = ?
	ORDER BY event_index`, id)
	if err != nil {
		return nil, fmt.Errorf("load source events: %w", err)
	}
	defer closeRows(rows)

	var events []SourceEvent
	for rows.Next() {
		row, err := scanSourceEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source event: %w", err)
		}
		events = append(events, row.toSourceEvent())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source events: %w", err)
	}
	return events, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		return
	}
}

func scanSourceRow(scanner rowScanner) (sourceRow, error) {
	var row sourceRow
	var startedAt any
	var endedAt any
	var importedAt any
	if err := scanner.Scan(
		&row.ID,
		&row.Kind,
		&row.URI,
		&row.ContentSHA256,
		&row.ScopeKind,
		&row.ScopeValue,
		&startedAt,
		&endedAt,
		&importedAt,
		&row.MetadataJSON,
	); err != nil {
		return sourceRow{}, err
	}
	var err error
	if row.StartedAt, err = scanOptionalTime(startedAt); err != nil {
		return sourceRow{}, fmt.Errorf("parse started_at: %w", err)
	}
	if row.EndedAt, err = scanOptionalTime(endedAt); err != nil {
		return sourceRow{}, fmt.Errorf("parse ended_at: %w", err)
	}
	if row.ImportedAt, err = scanRequiredTime(importedAt); err != nil {
		return sourceRow{}, fmt.Errorf("parse imported_at: %w", err)
	}
	return row, nil
}

func scanSourceEventRow(scanner rowScanner) (sourceEventRow, error) {
	var row sourceEventRow
	var at any
	if err := scanner.Scan(
		&row.SourceID,
		&row.Index,
		&row.Line,
		&at,
		&row.Type,
		&row.TurnID,
		&row.Role,
		&row.Text,
		&row.PayloadJSON,
		&row.RawJSON,
	); err != nil {
		return sourceEventRow{}, err
	}
	var err error
	if row.At, err = scanOptionalTime(at); err != nil {
		return sourceEventRow{}, fmt.Errorf("parse at: %w", err)
	}
	return row, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func scanOptionalTime(value any) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	return scanRequiredTime(value)
}

func scanRequiredTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		return parseSQLiteTime(v)
	case []byte:
		return parseSQLiteTime(string(v))
	default:
		return time.Time{}, fmt.Errorf("unsupported time value %T", value)
	}
}

func parseSQLiteTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", value)
}
