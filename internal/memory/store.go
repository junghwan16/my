package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/jsonutil"
	"github.com/junghwan16/my/internal/source"
)

// The store row-model mappings live in rows.go; the schema lives in the
// migrate package's ledger.

// defaultSearchLimit caps recall results when the caller passes a non-positive
// limit, so a search never dumps the whole store.
const defaultSearchLimit = 20

// Store records memories and their links back to sources, and maintains the
// full-text index used for recall.
type Store struct {
	db        *bun.DB
	tokenizer Tokenizer
}

var (
	_ MemoryWriter = (*Store)(nil)
	_ MemoryReader = (*Store)(nil)
)

// NewStore returns a memory store backed by an already-open database. The
// tokenizer indexes and queries memory text; it must be the same one on both
// paths so a term indexed one way is not missed by a query split differently.
func NewStore(db *bun.DB, tokenizer Tokenizer) *Store {
	return &Store{db: db, tokenizer: tokenizer}
}

// ReplaceSourceMemories atomically replaces every memory produced by one agent for a
// source. Memories linked to the source by the same agent that are absent from the
// new set are deleted, so re-ingesting a source never accumulates stale memories.
func (s *Store) ReplaceSourceMemories(ctx context.Context, sourceID source.SourceID, agent string, memories []Memory, links []Link) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := s.deleteStaleMemories(ctx, tx, sourceID, agent); err != nil {
			return err
		}
		if err := s.upsertMemories(ctx, tx, memories); err != nil {
			return err
		}
		return s.upsertLinks(ctx, tx, links)
	})
}

// deleteStaleMemories removes every memory (and its links and search rows) that
// the agent previously produced for the source, so a re-ingest never accumulates
// stale rows.
func (s *Store) deleteStaleMemories(ctx context.Context, tx bun.Tx, sourceID source.SourceID, agent string) error {
	var staleIDs []string
	if err := tx.NewSelect().
		Model((*memoryRow)(nil)).
		Column("memory.id").
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", sourceID).
		Where("memory.agent = ?", agent).
		Scan(ctx, &staleIDs); err != nil {
		return fmt.Errorf("load stale memories: %w", err)
	}
	if len(staleIDs) == 0 {
		return nil
	}
	if _, err := tx.NewDelete().
		Model((*linkRow)(nil)).
		Where("memory_id IN (?)", bun.List(staleIDs)).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete stale links: %w", err)
	}
	if _, err := tx.NewDelete().
		Model((*memoryRow)(nil)).
		Where("id IN (?)", bun.List(staleIDs)).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete stale memories: %w", err)
	}
	return s.deleteFTS(ctx, tx, staleIDs)
}

func (s *Store) upsertMemories(ctx context.Context, tx bun.Tx, memories []Memory) error {
	for i := range memories {
		mem := memories[i]
		if len(mem.MetadataJSON) == 0 {
			mem.MetadataJSON = jsonutil.EmptyObject()
		}
		if _, err := tx.NewInsert().
			Model(newMemoryRow(mem)).
			On("CONFLICT (id) DO UPDATE").
			Set("agent = EXCLUDED.agent").
			Set("kind = EXCLUDED.kind").
			Set("text = EXCLUDED.text").
			Set("created_at = EXCLUDED.created_at").
			Set("metadata_json = EXCLUDED.metadata_json").
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert memory: %w", err)
		}
		if err := s.indexFTS(ctx, tx, mem); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertLinks(ctx context.Context, tx bun.Tx, links []Link) error {
	for i := range links {
		link := links[i]
		if link.MemoryID == "" {
			continue
		}
		if len(link.MetadataJSON) == 0 {
			link.MetadataJSON = jsonutil.EmptyObject()
		}
		if _, err := tx.NewInsert().
			Model(newLinkRow(link)).
			On("CONFLICT (source_id, memory_id, kind) DO UPDATE").
			Set("created_at = EXCLUDED.created_at").
			Set("metadata_json = EXCLUDED.metadata_json").
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert memory link: %w", err)
		}
	}
	return nil
}

// SourceHasAgentMemories reports whether a source already has memories from an agent.
func (s *Store) SourceHasAgentMemories(ctx context.Context, sourceID source.SourceID, agent string) (bool, error) {
	count, err := s.db.NewSelect().
		Model((*memoryRow)(nil)).
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", sourceID).
		Where("memory.agent = ?", agent).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("count source agent memories: %w", err)
	}
	return count > 0, nil
}

// SourceMemories lists memories linked to a source.
func (s *Store) SourceMemories(ctx context.Context, id source.SourceID) ([]Memory, error) {
	var rows []memoryRow
	if err := s.db.NewSelect().
		Model(&rows).
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", id).
		Order("memory.created_at", "memory.id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return memories, nil
}

// SourceLinks lists memory links for a source.
func (s *Store) SourceLinks(ctx context.Context, id source.SourceID) ([]Link, error) {
	var rows []linkRow
	if err := s.db.NewSelect().
		Model(&rows).
		Where("source_id = ?", id).
		Order("created_at", "memory_id", "kind").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source links: %w", err)
	}

	links := make([]Link, 0, len(rows))
	for _, row := range rows {
		links = append(links, row.toLink())
	}
	return links, nil
}

// SearchMemories returns memories whose text matches query, ranked by FTS5 BM25.
// The query is tokenized with the store's tokenizer so it splits the same way
// the index did. When scope is non-empty, only memories linked to a source in
// that scope are returned. A non-positive limit falls back to defaultSearchLimit.
func (s *Store) SearchMemories(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	match := ftsMatch(s.tokenizer.Tokenize(query))
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	sql := `SELECT m.id AS id, m.agent AS agent, m.kind AS kind, m.text AS text, m.created_at AS created_at, m.metadata_json AS metadata_json
		FROM (SELECT memory_id, rank FROM memories_fts WHERE memories_fts MATCH ?) AS f
		JOIN memories AS m ON m.id = f.memory_id`
	args := []any{match}
	if scope != "" {
		sql += `
		WHERE EXISTS (
			SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id
			WHERE l.memory_id = m.id AND sr.scope_value = ?)`
		args = append(args, scope)
	}
	sql += `
		ORDER BY f.rank, m.created_at, m.id
		LIMIT ?`
	args = append(args, limit)

	var rows []memoryRow
	if err := s.db.NewRaw(sql, args...).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return memories, nil
}

// ReindexMemories rebuilds the full-text index from every stored memory. It is
// idempotent and backfills memories recorded before the index existed.
func (s *Store) ReindexMemories(ctx context.Context) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts"); err != nil {
			return fmt.Errorf("clear fts index: %w", err)
		}
		var rows []memoryRow
		if err := tx.NewSelect().Model(&rows).Scan(ctx); err != nil {
			return fmt.Errorf("load memories for reindex: %w", err)
		}
		for _, row := range rows {
			if err := s.indexFTS(ctx, tx, row.toMemory()); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureFTSIndexed backfills the index once when it is empty but memories exist,
// so recall works on a store populated before the index was added. Once filled,
// the write path keeps it in sync and this becomes a cheap no-op.
func (s *Store) EnsureFTSIndexed(ctx context.Context) error {
	var ftsCount int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories_fts").Scan(&ftsCount); err != nil {
		return fmt.Errorf("count fts rows: %w", err)
	}
	if ftsCount > 0 {
		return nil
	}
	var memCount int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories").Scan(&memCount); err != nil {
		return fmt.Errorf("count memories: %w", err)
	}
	if memCount == 0 {
		return nil
	}
	return s.ReindexMemories(ctx)
}

// indexFTS replaces the search row for one memory inside an open transaction.
func (s *Store) indexFTS(ctx context.Context, tx bun.Tx, mem Memory) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts WHERE memory_id = ?", string(mem.ID)); err != nil {
		return fmt.Errorf("clear fts row: %w", err)
	}
	tokens := strings.Join(s.tokenizer.Tokenize(mem.Text), " ")
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO memories_fts (memory_id, tokens) VALUES (?, ?)", string(mem.ID), tokens,
	); err != nil {
		return fmt.Errorf("index memory for search: %w", err)
	}
	return nil
}

// deleteFTS removes search rows for memories being replaced, inside a transaction.
func (s *Store) deleteFTS(ctx context.Context, tx bun.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts WHERE memory_id IN (?)", bun.List(ids)); err != nil {
		return fmt.Errorf("delete stale fts rows: %w", err)
	}
	return nil
}

// ftsMatch builds an FTS5 MATCH expression from tokens: each token is quoted
// (so it is treated as a literal string, safe from FTS operator characters) and
// joined with spaces, which FTS5 reads as an implicit AND.
func ftsMatch(tokens []string) string {
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}
