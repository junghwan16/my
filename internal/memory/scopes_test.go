package memories_test

import (
	"context"
	"path/filepath"
	"testing"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// TestScopesReturnsDistinctSourceScopes proves the scope read-model backing the
// web selector: it lists each distinct Scope a Source lives in exactly once
// (deduplicating two Sources in the same Scope), ordered for stable output.
func TestScopesReturnsDistinctSourceScopes(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// Two Sources share /work/a, so it must collapse to one scope; /work/b is
	// distinct. Recorded out of order to prove the query sorts, not insertion.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a1", "/work/a"), "memory:a1", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a2", "/work/a"), "memory:a2", "종목 리포트")

	scopes, err := memories.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(scopes) != 2 {
		t.Fatalf("scopes = %d (%#v), want 2 distinct", len(scopes), scopes)
	}
	// Ordered by kind then value: /work/a before /work/b.
	if scopes[0].Value != "/work/a" || scopes[1].Value != "/work/b" {
		t.Fatalf("scope values = %q, %q; want /work/a, /work/b (sorted, deduped)", scopes[0].Value, scopes[1].Value)
	}
	if scopes[0].Kind != sourcespkg.ScopeKindWorkspace {
		t.Fatalf("scope kind = %q, want workspace", scopes[0].Kind)
	}
}

// TestScopesEmptyWhenNoSources proves an empty store yields an empty, non-nil
// scope list, so the web selector renders only its all-scopes option.
func TestScopesEmptyWhenNoSources(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	scopes, err := memories.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scopes == nil {
		t.Fatal("scopes is nil, want an empty slice")
	}
	if len(scopes) != 0 {
		t.Fatalf("scopes = %d, want 0 for an empty store", len(scopes))
	}
}

// TestRecallerExposesScopes proves the shared Recaller seam forwards the scope
// read-model, so the web surface reaches it through the same seam as recall.
func TestRecallerExposesScopes(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	scopes, err := memoriespkg.NewRecaller(memories).Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0].Value != "/work/a" {
		t.Fatalf("recaller scopes = %#v, want single /work/a", scopes)
	}
}
