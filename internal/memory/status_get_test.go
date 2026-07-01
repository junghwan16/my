package memories_test

import (
	"context"
	"path/filepath"
	"testing"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
)

func TestStatsCountsMemoriesAndIndexRows(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recaller := memoriespkg.NewRecaller(memories)

	empty, err := recaller.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Memories != 0 || empty.FTSRows != 0 || empty.Vectors != 0 {
		t.Fatalf("empty stats = %#v, want all zero", empty)
	}

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "날씨 정보")

	stats, err := recaller.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Memories != 2 {
		t.Fatalf("memories = %d, want 2", stats.Memories)
	}
	// Every write indexes the FTS row; no embedder is attached, so no vectors.
	if stats.FTSRows != 2 {
		t.Fatalf("fts rows = %d, want 2", stats.FTSRows)
	}
	if stats.Vectors != 0 {
		t.Fatalf("vectors = %d, want 0 (no embedder attached)", stats.Vectors)
	}
}

func TestGetReturnsRecordedMemoryWithSources(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")

	recaller := memoriespkg.NewRecaller(memories)

	got, found, err := recaller.Get(ctx, "memory:a")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("get reported not-found for a saved memory, want found")
	}
	if got.MemoryID != "memory:a" {
		t.Fatalf("memory id = %q, want memory:a", got.MemoryID)
	}
	if got.Text != "코스피 종목 분석 리포트" {
		t.Fatalf("text = %q, want the saved text", got.Text)
	}
	if got.Agent != "t" || got.Kind != memoriespkg.MemoryKindSummary {
		t.Fatalf("agent/kind = %q/%q, want t/summary", got.Agent, got.Kind)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(got.Sources))
	}
	if got.Sources[0].ID != "codex_session:a" {
		t.Fatalf("source id = %q, want codex_session:a", got.Sources[0].ID)
	}
	if got.Sources[0].Scope.Value != "/work/a" {
		t.Fatalf("source scope value = %q, want /work/a", got.Sources[0].Scope.Value)
	}
}

func TestGetReportsNotFoundForUnknownID(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	got, found, err := memoriespkg.NewRecaller(memories).Get(ctx, "memory:missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatalf("get reported found for an unknown id, want not-found; got %#v", got)
	}
	if got.MemoryID != "" {
		t.Fatalf("not-found recall result = %#v, want zero value", got)
	}
}
