package memory

import (
	"context"
	"strings"

	"github.com/junghwan16/gieok/internal/source"
)

// MemoryReader loads recorded memories and searches them. *Store satisfies it.
type MemoryReader interface {
	SourceMemories(context.Context, source.SourceID) ([]Memory, error)
	SourceLinks(context.Context, source.SourceID) ([]Link, error)
	SearchMemories(ctx context.Context, query, scope string, limit int) ([]Memory, error)
	SearchSemantic(ctx context.Context, query, scope string, limit int) ([]Memory, error)
	SearchRecollections(ctx context.Context, query, scope string, limit int) ([]Recollection, error)
	HybridRecollections(ctx context.Context, query, scope string, limit int) ([]Recollection, error)
	RecentRecollections(ctx context.Context, scope string, limit int) ([]Recollection, error)
	RecollectByID(ctx context.Context, id MemoryID) (Recollection, bool, error)
	Stats(ctx context.Context) (Stats, error)
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

// SearchSemantic returns memories relevant to query ranked by embedding cosine
// similarity (best-first), optionally restricted to a scope. It is the semantic
// counterpart to Search and the engine a later hybrid recall (#6) fuses with
// the lexical ranking. When the store has no embedder configured it returns no
// results and no error, so callers can fall back to lexical Search. An empty
// scope searches every scope.
func (r *Recaller) SearchSemantic(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	return r.store.SearchSemantic(ctx, query, scope, limit)
}

// Links returns the source-to-memory links for a source.
func (r *Recaller) Links(ctx context.Context, id source.SourceID) ([]Link, error) {
	return r.store.SourceLinks(ctx, id)
}

// Recollect is the recall application seam the CLI and the MCP tool share. It
// finds relevant Memory within a Scope for the current task and returns
// Recollections carrying Source context. With task text it ranks by hybrid
// recall — fusing the lexical (FTS5/BM25) and semantic (embedding cosine)
// rankings with Reciprocal Rank Fusion — so the two engines' complementary hits
// reinforce each other. When no embedder is attached the semantic list is empty
// and the fusion degrades to pure lexical order, so recall keeps working
// offline with no contract change. With empty task text it returns recent
// Memory in scope, so recall doubles as a workspace memory overview. An empty
// scope spans every scope.
func (r *Recaller) Recollect(ctx context.Context, task, scope string, limit int) ([]Recollection, error) {
	if strings.TrimSpace(task) == "" {
		return r.store.RecentRecollections(ctx, scope, limit)
	}
	return r.store.HybridRecollections(ctx, task, scope, limit)
}

// Get fetches one Memory by id and attaches its Source context, reusing the
// shared recollection shape so the MCP get tool matches recall. found is false
// (with a zero Recollection and no error) when no Memory has the id, so callers
// render a clean "not found" rather than surfacing an error.
func (r *Recaller) Get(ctx context.Context, id MemoryID) (Recollection, bool, error) {
	return r.store.RecollectByID(ctx, id)
}

// Stats reports recall index health (memory, vector, and full-text index
// counts) so the MCP status tool can surface it. It delegates to the store.
func (r *Recaller) Stats(ctx context.Context) (Stats, error) {
	return r.store.Stats(ctx)
}
