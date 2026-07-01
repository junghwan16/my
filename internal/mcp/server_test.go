package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/junghwan16/gieok/internal/mcp"
	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
	"github.com/junghwan16/gieok/internal/tokenize"
)

func TestRecallReturnsRankedMemoryWithSource(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "오늘 날씨 정보")

	server := mcp.NewServer(memoriespkg.NewRecaller(memories))

	out, err := server.Recall(ctx, mcp.RecallInput{Query: "종목"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 1 {
		t.Fatalf("recalled %d memories, want 1 (only the matching one)", len(out.Memories))
	}

	got := out.Memories[0]
	if got.MemoryID != "memory:a" {
		t.Fatalf("memory id = %q, want memory:a", got.MemoryID)
	}
	if got.Text != "코스피 종목 분석 리포트" {
		t.Fatalf("text = %q, want the saved memory text", got.Text)
	}
	if got.Agent != "t" || got.Kind != string(memoriespkg.MemoryKindSummary) {
		t.Fatalf("agent/kind = %q/%q, want t/summary", got.Agent, got.Kind)
	}
	if got.Created == "" {
		t.Fatal("created is empty, want an RFC3339 timestamp")
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(got.Sources))
	}
	src := got.Sources[0]
	if src.ID != "codex_session:a" {
		t.Fatalf("source id = %q, want codex_session:a", src.ID)
	}
	if src.URI != "memory://test/codex_session:a" {
		t.Fatalf("source uri = %q, want the source URI", src.URI)
	}
	if src.Scope.Value != "/work/a" {
		t.Fatalf("source scope value = %q, want /work/a", src.Scope.Value)
	}
	if src.Scope.Kind != string(sourcespkg.ScopeKindWorkspace) {
		t.Fatalf("source scope kind = %q, want workspace", src.Scope.Kind)
	}
}

func TestRecallHonorsScope(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")

	server := mcp.NewServer(memoriespkg.NewRecaller(memories))

	scoped, err := server.Recall(ctx, mcp.RecallInput{Query: "종목", Scope: "/work/a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Memories) != 1 || scoped.Memories[0].MemoryID != "memory:a" {
		t.Fatalf("scoped recall = %#v, want single memory:a", scoped.Memories)
	}

	all, err := server.Recall(ctx, mcp.RecallInput{Query: "종목"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Memories) != 2 {
		t.Fatalf("unscoped recall = %d, want 2", len(all.Memories))
	}
}

func TestRecallHonorsLimit(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 하나")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/a"), "memory:b", "종목 둘")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:c", "/work/a"), "memory:c", "종목 셋")

	server := mcp.NewServer(memoriespkg.NewRecaller(memories))

	out, err := server.Recall(ctx, mcp.RecallInput{Query: "종목", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 2 {
		t.Fatalf("recalled %d memories, want 2 (limit honored)", len(out.Memories))
	}
}

func TestRecallEmptyQueryErrors(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := mcp.NewServer(memoriespkg.NewRecaller(memories))

	if _, err := server.Recall(ctx, mcp.RecallInput{Query: ""}); err == nil {
		t.Fatal("empty query returned no error, want an error")
	}
}

// --- store-backed test helpers (mirror internal/memory test setup) ---

func openStores(ctx context.Context, t *testing.T, path string) (*sourcespkg.Store, *memoriespkg.Store, func()) {
	t.Helper()
	tok, err := tokenize.NewKorean()
	if err != nil {
		t.Fatal(err)
	}
	return openStoresWith(ctx, t, path, tok)
}

func openStoresWith(ctx context.Context, t *testing.T, path string, tok memoriespkg.Tokenizer) (*sourcespkg.Store, *memoriespkg.Store, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	return sourcespkg.NewStore(db), memoriespkg.NewStore(db, tok), closeDB(t, db)
}

func closeDB(t *testing.T, db *sql.DB) func() {
	return func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func scopedSource(id sourcespkg.SourceID, scope string) sourcespkg.Source {
	return sourcespkg.Source{
		ID:            id,
		Kind:          sourcespkg.SourceKindCodexSession,
		URI:           "memory://test/" + string(id),
		ContentSHA256: "hash-" + string(id),
		Scope:         sourcespkg.Scope{Kind: sourcespkg.ScopeKindWorkspace, Value: scope},
		ImportedAt:    time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		MetadataJSON:  json.RawMessage(`{}`),
	}
}

func recordMemory(ctx context.Context, t *testing.T, sources *sourcespkg.Store, memories *memoriespkg.Store, src sourcespkg.Source, memID, text string) {
	t.Helper()
	if err := sources.SaveSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	mem := memoriespkg.Memory{ID: memoriespkg.MemoryID(memID), Agent: "t", Kind: memoriespkg.MemoryKindSummary, Text: text, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	link := memoriespkg.Link{SourceID: src.ID, MemoryID: mem.ID, Kind: memoriespkg.LinkKindSourceIngest, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	if err := memories.ReplaceSourceMemories(ctx, src.ID, "t", []memoriespkg.Memory{mem}, []memoriespkg.Link{link}, nil); err != nil {
		t.Fatal(err)
	}
}
