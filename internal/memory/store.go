package memory

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/jsonutil"
	"github.com/junghwan16/my/internal/source"
)

// The store schema and row-model mappings live in schema.go.

// Store records memories and their links back to sources.
type Store struct {
	db *bun.DB
}

var (
	_ MemoryWriter = (*Store)(nil)
	_ MemoryReader = (*Store)(nil)
)

// NewStore returns a memory store backed by an already-open database.
func NewStore(db *bun.DB) *Store {
	return &Store{db: db}
}

// ReplaceSourceMemories atomically replaces every memory produced by one agent for a
// source. Memories linked to the source by the same agent that are absent from the
// new set are deleted, so re-ingesting a source never accumulates stale memories.
func (s *Store) ReplaceSourceMemories(ctx context.Context, sourceID source.SourceID, agent string, memories []Memory, links []Link) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
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
		if len(staleIDs) > 0 {
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
		}

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
		}

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
	})
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
