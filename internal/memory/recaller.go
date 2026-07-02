package memories

import (
	"context"
	"strings"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// MemoryReader loads saved memories and searches them. *Store satisfies it.
type MemoryReader interface {
	SourceMemories(context.Context, sourcespkg.SourceID) ([]Memory, error)
	SourceLinks(context.Context, sourcespkg.SourceID) ([]Link, error)
	SearchMemories(ctx context.Context, query, scope string, limit int) ([]Memory, error)
	SearchSemantic(ctx context.Context, query, scope string, limit int) ([]Memory, error)
	SearchRecallResults(ctx context.Context, query, scope string, limit int) ([]RecallResult, error)
	HybridRecallResults(ctx context.Context, query, scope string, limit int) ([]RecallResult, error)
	RecentRecallResults(ctx context.Context, scope string, limit int) ([]RecallResult, error)
	RecallResultByID(ctx context.Context, id MemoryID) (RecallResult, bool, error)
	Scopes(ctx context.Context) ([]sourcespkg.Scope, error)
	Stats(ctx context.Context) (Stats, error)
	Graph(ctx context.Context, scope string, cap int) (Graph, error)
	MemoryNeighborhood(ctx context.Context, id MemoryID) (Graph, bool, error)
}

// Recaller finds relevant memory. It can load the memories linked to one Source,
// search Memory text, or run the product-level Recall flow within a Scope.
type Recaller struct {
	store MemoryReader
}

// NewRecaller returns a Recaller backed by a memory reader.
func NewRecaller(store MemoryReader) *Recaller {
	return &Recaller{store: store}
}

// SourceMemories returns the memories linked to a source.
func (r *Recaller) SourceMemories(ctx context.Context, id sourcespkg.SourceID) ([]Memory, error) {
	return r.store.SourceMemories(ctx, id)
}

// Search returns memories relevant to query, ranked best-first, optionally
// restricted to a scope. An empty scope searches every scope.
func (r *Recaller) Search(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	return r.store.SearchMemories(ctx, query, scope, limit)
}

// SearchSemantic returns memories relevant to query ranked by embedding cosine
// similarity (best-first), optionally restricted to a scope. It is the semantic
// counterpart to Search and one input to hybrid Recall. When the store has no
// embedder configured it returns no results and no error, so callers can fall
// back to lexical Search. An empty scope searches every scope.
func (r *Recaller) SearchSemantic(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	return r.store.SearchSemantic(ctx, query, scope, limit)
}

// Links returns the source-to-memory links for a source.
func (r *Recaller) Links(ctx context.Context, id sourcespkg.SourceID) ([]Link, error) {
	return r.store.SourceLinks(ctx, id)
}

// Recall is the recall entry point the CLI and the MCP tool share. It
// finds relevant Memory within a Scope for the current task and returns
// RecallResults carrying Source context. With task text it ranks by hybrid
// recall — fusing the lexical (FTS5/BM25) and semantic (embedding cosine)
// rankings with Reciprocal Rank Fusion — so the two engines' complementary hits
// reinforce each other. When no embedder is attached the semantic list is empty
// and the fusion degrades to pure lexical order, so recall keeps working
// offline with no contract change. With empty task text it returns recent
// Memory in scope, so recall doubles as a workspace memory overview. An empty
// scope spans every scope.
func (r *Recaller) Recall(ctx context.Context, task, scope string, limit int) ([]RecallResult, error) {
	if strings.TrimSpace(scope) == "" {
		// All-scopes: no workspace-key filtering, keep the store's global ranking.
		return r.rank(ctx, task, "", limit)
	}
	// Scoped recall matches by workspace key, not by the raw cwd string, so a
	// project's memory is found across its renamed/worktree/conductor paths
	// (ADR-0009). Rank over every scope, then keep the top results whose Source
	// shares the query's workspace key. Ranking stays global (the store's), we
	// only filter — so this never reorders, it just selects the right workspace.
	over := limit * scopeOverfetchFactor
	if over < scopeOverfetchMin {
		over = scopeOverfetchMin
	}
	ranked, err := r.rank(ctx, task, "", over)
	if err != nil {
		return nil, err
	}
	key := sourcespkg.WorkspaceKey(scope, sourcespkg.DefaultScopeAliases())
	kept := make([]RecallResult, 0, limit)
	for _, result := range ranked {
		if resultMatchesWorkspaceKey(result, key) {
			kept = append(kept, result)
			if len(kept) == limit {
				break
			}
		}
	}
	return kept, nil
}

// scopeOverfetchFactor and scopeOverfetchMin size the candidate pool for a scoped
// recall: rank this many across all scopes before filtering to the workspace key.
// It must comfortably exceed the query's in-key hits; at the local scale (a few
// thousand memories) a fixed floor is enough. A store-side scope_key column
// (ADR-0009) would remove the over-fetch entirely at larger scale.
const (
	scopeOverfetchFactor = 10
	scopeOverfetchMin    = 100
)

// rank returns the store's ranking for the task within scope: recent memory when
// the task is blank, hybrid RRF otherwise. It is the single ranking entry point
// Recall filters on.
func (r *Recaller) rank(ctx context.Context, task, scope string, limit int) ([]RecallResult, error) {
	if strings.TrimSpace(task) == "" {
		return r.store.RecentRecallResults(ctx, scope, limit)
	}
	return r.store.HybridRecallResults(ctx, task, scope, limit)
}

// resultMatchesWorkspaceKey reports whether any Source a result derives from
// shares the given workspace key, so scoped recall keeps a memory reachable from
// its project regardless of which raw path (rename/worktree/conductor) it was
// captured under.
func resultMatchesWorkspaceKey(result RecallResult, key string) bool {
	aliases := sourcespkg.DefaultScopeAliases()
	for _, source := range result.Sources {
		if sourcespkg.WorkspaceKey(source.Scope.Value, aliases) == key {
			return true
		}
	}
	return false
}

// Get fetches one Memory by id and attaches its Source context, reusing the
// shared recall result structure so the MCP get tool matches recall. found is false
// (with a zero RecallResult and no error) when no Memory has the id, so callers
// render a clean "not found" rather than surfacing an error.
func (r *Recaller) Get(ctx context.Context, id MemoryID) (RecallResult, bool, error) {
	return r.store.RecallResultByID(ctx, id)
}

// Scopes lists the distinct Scopes any Source lives in, ordered for stable
// output. It backs the web scope selector: each value is one the recall path
// filters on, and the empty scope (all-scopes) is left for the surface to add.
// It delegates to the store and never touches the recall path.
func (r *Recaller) Scopes(ctx context.Context) ([]sourcespkg.Scope, error) {
	return r.store.Scopes(ctx)
}

// Stats reports recall index health (memory, vector, and full-text index
// counts) so the MCP status tool can report it. It delegates to the store.
func (r *Recaller) Stats(ctx context.Context) (Stats, error) {
	return r.store.Stats(ctx)
}

// Graph builds the scope-scoped provenance graph (Source and Memory nodes with
// Link edges, per-Memory fan-in, and the aggregate panel) for the web /graph
// page. It delegates to the store's read-model and never touches the recall
// path. An empty scope spans every scope; a non-positive cap uses the store
// default.
func (r *Recaller) Graph(ctx context.Context, scope string, cap int) (Graph, error) {
	return r.store.Graph(ctx, scope, cap)
}

// MemoryNeighborhood is the click-to-expand drilldown backing the /graph page:
// one Memory plus the Sources that melted into it and their Link edges,
// unrestricted by scope. found is false when no Memory has the id. It delegates
// to the store's read-model.
func (r *Recaller) MemoryNeighborhood(ctx context.Context, id MemoryID) (Graph, bool, error) {
	return r.store.MemoryNeighborhood(ctx, id)
}
