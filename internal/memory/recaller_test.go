package memories

import (
	"context"
	"testing"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// fakeReader is a minimal MemoryReader for exercising Recall's scope-key
// filtering. Only the ranking methods Recall uses carry behavior; the rest
// satisfy the interface with zero values.
type fakeReader struct {
	hybrid map[string][]RecallResult // keyed by the scope passed to HybridRecallResults
	recent map[string][]RecallResult
}

func (f fakeReader) HybridRecallResults(_ context.Context, _ string, scope string, limit int) ([]RecallResult, error) {
	return capResults(f.hybrid[scope], limit), nil
}

func (f fakeReader) RecentRecallResults(_ context.Context, scope string, limit int) ([]RecallResult, error) {
	return capResults(f.recent[scope], limit), nil
}

func capResults(results []RecallResult, limit int) []RecallResult {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}

// Unused-by-Recall interface methods.
func (fakeReader) SourceMemories(context.Context, sourcespkg.SourceID) ([]Memory, error) {
	return nil, nil
}
func (fakeReader) SourceLinks(context.Context, sourcespkg.SourceID) ([]Link, error) { return nil, nil }
func (fakeReader) SearchMemories(context.Context, string, string, int) ([]Memory, error) {
	return nil, nil
}
func (fakeReader) SearchSemantic(context.Context, string, string, int) ([]Memory, error) {
	return nil, nil
}
func (fakeReader) SearchRecallResults(context.Context, string, string, int) ([]RecallResult, error) {
	return nil, nil
}
func (fakeReader) RecallResultByID(context.Context, MemoryID) (RecallResult, bool, error) {
	return RecallResult{}, false, nil
}
func (fakeReader) Scopes(context.Context) ([]sourcespkg.Scope, error) { return nil, nil }
func (fakeReader) Stats(context.Context) (Stats, error)               { return Stats{}, nil }
func (fakeReader) Graph(context.Context, string, int) (Graph, error)  { return Graph{}, nil }
func (fakeReader) MemoryNeighborhood(context.Context, MemoryID) (Graph, bool, error) {
	return Graph{}, false, nil
}

func resultInScope(id, scopeValue string) RecallResult {
	return RecallResult{
		MemoryID: MemoryID(id),
		Text:     "text " + id,
		Sources:  []SourceRef{{Scope: sourcespkg.Scope{Kind: sourcespkg.ScopeKindWorkspace, Value: scopeValue}}},
	}
}

// TestRecallMatchesAcrossWorkspaceKey is the end-to-end guard for ADR-0009: a
// scoped recall from the canonical path must return memory captured under the
// project's other paths (the pre-rename path, a worktree), because they share a
// workspace key. Previously exact-scope matching returned nothing from the
// canonical path (measured: canonical=0 vs /my=3).
func TestRecallMatchesAcrossWorkspaceKey(t *testing.T) {
	// All memory was captured under the legacy /my path; ranking spans all scopes.
	all := []RecallResult{
		resultInScope("m1", "/Users/jeff.cho/personal/my"),
		resultInScope("m2", "/Users/jeff.cho/personal/my/.claude/worktrees/x"),
		resultInScope("other", "/Users/jeff.cho/Projects/adserver"),
	}
	reader := fakeReader{hybrid: map[string][]RecallResult{"": all}}
	rec := NewRecaller(reader)

	// Query from the CANONICAL path, where nothing was directly captured.
	got, err := rec.Recall(context.Background(), "task", "/Users/jeff.cho/personal/gieok", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("recalled %d, want 2 (both /my and its worktree share the canonical key)", len(got))
	}
	for _, r := range got {
		if r.MemoryID == "other" {
			t.Fatalf("adserver memory leaked into the gieok workspace recall")
		}
	}
}

func TestRecallAllScopesIsUnfiltered(t *testing.T) {
	all := []RecallResult{
		resultInScope("m1", "/Users/jeff.cho/personal/my"),
		resultInScope("other", "/Users/jeff.cho/Projects/adserver"),
	}
	reader := fakeReader{hybrid: map[string][]RecallResult{"": all}}
	rec := NewRecaller(reader)

	got, err := rec.Recall(context.Background(), "task", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("all-scopes recall = %d, want 2 (no key filtering)", len(got))
	}
}

func TestRecallScopedDefaultsLimitWhenNonPositive(t *testing.T) {
	// A non-positive limit (the CLI default when --limit is omitted) must cap the
	// scoped path at the store default, not spill the whole over-fetch.
	all := make([]RecallResult, 0, defaultSearchLimit+10)
	for i := 0; i < defaultSearchLimit+10; i++ {
		all = append(all, resultInScope(string(rune('a'+i%26))+"-", "/Users/jeff.cho/personal/gieok"))
	}
	reader := fakeReader{hybrid: map[string][]RecallResult{"": all}}
	rec := NewRecaller(reader)

	got, err := rec.Recall(context.Background(), "task", "/Users/jeff.cho/personal/gieok", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != defaultSearchLimit {
		t.Fatalf("scoped recall with limit=0 returned %d, want the store default %d", len(got), defaultSearchLimit)
	}
}

func TestRecallScopedRespectsLimitAfterFilter(t *testing.T) {
	all := []RecallResult{
		resultInScope("m1", "/Users/jeff.cho/personal/my"),
		resultInScope("other", "/Users/jeff.cho/Projects/adserver"),
		resultInScope("m2", "/Users/jeff.cho/personal/gieok"),
		resultInScope("m3", "/Users/jeff.cho/personal/my"),
	}
	reader := fakeReader{hybrid: map[string][]RecallResult{"": all}}
	rec := NewRecaller(reader)

	got, err := rec.Recall(context.Background(), "task", "/Users/jeff.cho/personal/gieok", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("recalled %d, want 2 (limit applied after key filter)", len(got))
	}
	if got[0].MemoryID != "m1" || got[1].MemoryID != "m2" {
		t.Fatalf("kept %q,%q; want m1,m2 (ranking order preserved, 'other' filtered out)", got[0].MemoryID, got[1].MemoryID)
	}
}
