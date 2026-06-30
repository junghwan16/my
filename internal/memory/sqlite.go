package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// schemaSQL is the single source of truth for the memory store schema. The row
// models below map onto these tables; keep the two in sync here.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS sources (
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
);

CREATE UNIQUE INDEX IF NOT EXISTS sources_kind_hash_idx
	ON sources (kind, content_sha256);

CREATE TABLE IF NOT EXISTS source_events (
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
);

CREATE TABLE IF NOT EXISTS memory_items (
	id TEXT PRIMARY KEY,
	agent TEXT NOT NULL,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memory_items_agent_idx ON memory_items (agent);

CREATE TABLE IF NOT EXISTS memory_links (
	source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	item_id TEXT NOT NULL REFERENCES memory_items(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	PRIMARY KEY (source_id, item_id, kind)
);
`

// Migrate creates the memory store tables. It is idempotent.
func Migrate(ctx context.Context, db *bun.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate sqlite database: %w", err)
	}
	return nil
}

// Store records imported memory sources and their normalized events.
type Store struct {
	db *bun.DB
}

// NewStore returns a memory store backed by an already-open database.
func NewStore(db *bun.DB) *Store {
	return &Store{db: db}
}

// RecordSource replaces a source and all of its events atomically.
func (s *Store) RecordSource(ctx context.Context, source Source, events []SourceEvent) error {
	if len(source.MetadataJSON) == 0 {
		source.MetadataJSON = jsonObject()
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

// ReplaceSourceItems atomically replaces every item produced by one agent for a
// source. Items linked to the source by the same agent that are absent from the
// new set are deleted, so re-ingesting a source never accumulates stale items.
func (s *Store) ReplaceSourceItems(ctx context.Context, sourceID SourceID, agent string, items []Item, links []Link) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var staleIDs []string
		if err := tx.NewSelect().
			Model((*itemRow)(nil)).
			Column("item.id").
			Join("JOIN memory_links AS link ON link.item_id = item.id").
			Where("link.source_id = ?", sourceID).
			Where("item.agent = ?", agent).
			Scan(ctx, &staleIDs); err != nil {
			return fmt.Errorf("load stale items: %w", err)
		}
		if len(staleIDs) > 0 {
			if _, err := tx.NewDelete().
				Model((*linkRow)(nil)).
				Where("item_id IN (?)", bun.List(staleIDs)).
				Exec(ctx); err != nil {
				return fmt.Errorf("delete stale links: %w", err)
			}
			if _, err := tx.NewDelete().
				Model((*itemRow)(nil)).
				Where("id IN (?)", bun.List(staleIDs)).
				Exec(ctx); err != nil {
				return fmt.Errorf("delete stale items: %w", err)
			}
		}

		for i := range items {
			item := items[i]
			if len(item.MetadataJSON) == 0 {
				item.MetadataJSON = jsonObject()
			}
			if _, err := tx.NewInsert().
				Model(newItemRow(item)).
				On("CONFLICT (id) DO UPDATE").
				Set("agent = EXCLUDED.agent").
				Set("kind = EXCLUDED.kind").
				Set("text = EXCLUDED.text").
				Set("created_at = EXCLUDED.created_at").
				Set("metadata_json = EXCLUDED.metadata_json").
				Exec(ctx); err != nil {
				return fmt.Errorf("upsert memory item: %w", err)
			}
		}

		for i := range links {
			link := links[i]
			if link.ItemID == "" {
				continue
			}
			if len(link.MetadataJSON) == 0 {
				link.MetadataJSON = jsonObject()
			}
			if _, err := tx.NewInsert().
				Model(newLinkRow(link)).
				On("CONFLICT (source_id, item_id, kind) DO UPDATE").
				Set("created_at = EXCLUDED.created_at").
				Set("metadata_json = EXCLUDED.metadata_json").
				Exec(ctx); err != nil {
				return fmt.Errorf("upsert memory link: %w", err)
			}
		}
		return nil
	})
}

// SourceHasAgentItems reports whether a source already has items from an agent.
func (s *Store) SourceHasAgentItems(ctx context.Context, sourceID SourceID, agent string) (bool, error) {
	count, err := s.db.NewSelect().
		Model((*itemRow)(nil)).
		Join("JOIN memory_links AS link ON link.item_id = item.id").
		Where("link.source_id = ?", sourceID).
		Where("item.agent = ?", agent).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("count source agent items: %w", err)
	}
	return count > 0, nil
}

// SourceItems lists memory items linked to a source.
func (s *Store) SourceItems(ctx context.Context, id SourceID) ([]Item, error) {
	var rows []itemRow
	if err := s.db.NewSelect().
		Model(&rows).
		Join("JOIN memory_links AS link ON link.item_id = item.id").
		Where("link.source_id = ?", id).
		Order("item.created_at", "item.id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source items: %w", err)
	}

	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.toItem())
	}
	return items, nil
}

// SourceLinks lists memory links for a source.
func (s *Store) SourceLinks(ctx context.Context, id SourceID) ([]Link, error) {
	var rows []linkRow
	if err := s.db.NewSelect().
		Model(&rows).
		Where("source_id = ?", id).
		Order("created_at", "item_id", "kind").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source links: %w", err)
	}

	links := make([]Link, 0, len(rows))
	for _, row := range rows {
		links = append(links, row.toLink())
	}
	return links, nil
}

type sourceRow struct {
	bun.BaseModel `bun:"table:sources"`

	ID            string    `bun:"id,pk"`
	Kind          string    `bun:"kind,notnull"`
	URI           string    `bun:"uri,notnull"`
	ContentSHA256 string    `bun:"content_sha256,notnull"`
	ScopeKind     string    `bun:"scope_kind,notnull"`
	ScopeValue    string    `bun:"scope_value,notnull"`
	StartedAt     time.Time `bun:"started_at,nullzero"`
	EndedAt       time.Time `bun:"ended_at,nullzero"`
	RecordedAt    time.Time `bun:"recorded_at,notnull"`
	MetadataJSON  string    `bun:"metadata_json,notnull"`
}

type sourceEventRow struct {
	bun.BaseModel `bun:"table:source_events"`

	SourceID    string    `bun:"source_id,pk"`
	Index       int       `bun:"event_index,pk"`
	Line        int       `bun:"line,notnull"`
	At          time.Time `bun:"at,nullzero"`
	Type        string    `bun:"type,notnull"`
	TurnID      string    `bun:"turn_id,notnull"`
	Role        string    `bun:"role,notnull"`
	Text        string    `bun:"text,notnull"`
	PayloadJSON string    `bun:"payload_json,notnull"`
	RawJSON     string    `bun:"raw_json,notnull"`
}

type itemRow struct {
	bun.BaseModel `bun:"table:memory_items,alias:item"`

	ID           string    `bun:"id,pk"`
	Agent        string    `bun:"agent,notnull"`
	Kind         string    `bun:"kind,notnull"`
	Text         string    `bun:"text,notnull"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	MetadataJSON string    `bun:"metadata_json,notnull"`
}

type linkRow struct {
	bun.BaseModel `bun:"table:memory_links,alias:link"`

	SourceID     string    `bun:"source_id,pk"`
	ItemID       string    `bun:"item_id,pk"`
	Kind         string    `bun:"kind,pk"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	MetadataJSON string    `bun:"metadata_json,notnull"`
}

func newSourceRow(source Source) *sourceRow {
	return &sourceRow{
		ID:            string(source.ID),
		Kind:          string(source.Kind),
		URI:           source.URI,
		ContentSHA256: source.ContentSHA256,
		ScopeKind:     string(source.Scope.Kind),
		ScopeValue:    source.Scope.Value,
		StartedAt:     source.StartedAt,
		EndedAt:       source.EndedAt,
		RecordedAt:    source.RecordedAt,
		MetadataJSON:  string(source.MetadataJSON),
	}
}

func (r sourceRow) toSource() Source {
	return Source{
		ID:            SourceID(r.ID),
		Kind:          SourceKind(r.Kind),
		URI:           r.URI,
		ContentSHA256: r.ContentSHA256,
		Scope: Scope{
			Kind:  ScopeKind(r.ScopeKind),
			Value: r.ScopeValue,
		},
		StartedAt:    r.StartedAt,
		EndedAt:      r.EndedAt,
		RecordedAt:   r.RecordedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

func newItemRow(item Item) *itemRow {
	return &itemRow{
		ID:           string(item.ID),
		Agent:        item.Agent,
		Kind:         string(item.Kind),
		Text:         item.Text,
		CreatedAt:    item.CreatedAt,
		MetadataJSON: string(item.MetadataJSON),
	}
}

func (r itemRow) toItem() Item {
	return Item{
		ID:           ItemID(r.ID),
		Agent:        r.Agent,
		Kind:         ItemKind(r.Kind),
		Text:         r.Text,
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

func newLinkRow(link Link) *linkRow {
	return &linkRow{
		SourceID:     string(link.SourceID),
		ItemID:       string(link.ItemID),
		Kind:         string(link.Kind),
		CreatedAt:    link.CreatedAt,
		MetadataJSON: string(link.MetadataJSON),
	}
}

func (r linkRow) toLink() Link {
	return Link{
		SourceID:     SourceID(r.SourceID),
		ItemID:       ItemID(r.ItemID),
		Kind:         LinkKind(r.Kind),
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

func newSourceEventRow(event SourceEvent) sourceEventRow {
	if len(event.PayloadJSON) == 0 {
		event.PayloadJSON = jsonObject()
	}
	if len(event.RawJSON) == 0 {
		event.RawJSON = jsonObject()
	}

	return sourceEventRow{
		SourceID:    string(event.SourceID),
		Index:       event.Index,
		Line:        event.Line,
		At:          event.At,
		Type:        event.Type,
		TurnID:      event.TurnID,
		Role:        event.Role,
		Text:        event.Text,
		PayloadJSON: string(event.PayloadJSON),
		RawJSON:     string(event.RawJSON),
	}
}

func (r sourceEventRow) toSourceEvent() SourceEvent {
	return SourceEvent{
		SourceID:    SourceID(r.SourceID),
		Index:       r.Index,
		Line:        r.Line,
		At:          r.At,
		Type:        r.Type,
		TurnID:      r.TurnID,
		Role:        r.Role,
		Text:        r.Text,
		PayloadJSON: []byte(r.PayloadJSON),
		RawJSON:     []byte(r.RawJSON),
	}
}
