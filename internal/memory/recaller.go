package memory

import (
	"context"

	"github.com/junghwan16/my/internal/source"
)

// MemoryReader loads recorded memories and searches them. *Store satisfies it.
type MemoryReader interface {
	SourceMemories(context.Context, source.SourceID) ([]Memory, error)
	SourceLinks(context.Context, source.SourceID) ([]Link, error)
	SearchMemories(ctx context.Context, query, scope string, limit int) ([]Memory, error)
}

// Recaller finds relevant memory. It recalls by source, and searches memory
// text within a scope (the domain's "Recall within a Scope"). Semantic and
// hybrid ranking are on the roadmap behind this same Search contract.
type Recaller struct {
	store MemoryReader
}

// NewRecaller returns a Recaller backed by a memory reader.
func NewRecaller(store MemoryReader) *Recaller {
	return &Recaller{store: store}
}

// Recall returns the memories linked to a source.
func (r *Recaller) Recall(ctx context.Context, id source.SourceID) ([]Memory, error) {
	return r.store.SourceMemories(ctx, id)
}

// Search returns memories relevant to query, ranked best-first, optionally
// restricted to a scope. An empty scope searches every scope.
func (r *Recaller) Search(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	return r.store.SearchMemories(ctx, query, scope, limit)
}

// Links returns the source-to-memory links for a source.
func (r *Recaller) Links(ctx context.Context, id source.SourceID) ([]Link, error) {
	return r.store.SourceLinks(ctx, id)
}
