package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/migrate"
	"github.com/junghwan16/my/internal/source"
	"github.com/junghwan16/my/internal/storage"
	"github.com/junghwan16/my/internal/tokenize"
)

func TestSearchReturnsOnlyMatchingMemory(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "오늘 날씨 정보")

	got, err := memory.NewRecaller(memories).Search(ctx, "종목", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("search results = %d, want 1 (only the matching memory)", len(got))
	}
	if got[0].ID != "memory:a" {
		t.Fatalf("matched memory = %q, want memory:a", got[0].ID)
	}
}

func TestSearchFiltersByScope(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")

	recaller := memory.NewRecaller(memories)

	scoped, err := recaller.Search(ctx, "종목", "/work/a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ID != "memory:a" {
		t.Fatalf("scoped search = %#v, want single memory:a", scoped)
	}

	all, err := recaller.Search(ctx, "종목", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped search = %d, want 2", len(all))
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 하나")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/a"), "memory:b", "종목 둘")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:c", "/work/a"), "memory:c", "종목 셋")

	got, err := memory.NewRecaller(memories).Search(ctx, "종목", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("search results = %d, want 2 (limit honored)", len(got))
	}
}

func TestSearchEmptyQueryReturnsNothing(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	recaller := memory.NewRecaller(memories)
	for _, q := range []string{"", "   "} {
		got, err := recaller.Search(ctx, q, "", 10)
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if len(got) != 0 {
			t.Fatalf("query %q results = %d, want 0", q, len(got))
		}
	}
}

func TestSearchReflectsReplacedMemory(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	src := scopedSource("codex_session:a", "/work/a")
	recordMemory(ctx, t, sources, memories, src, "memory:v1", "종목 분석")

	recaller := memory.NewRecaller(memories)
	if got, err := recaller.Search(ctx, "종목", "", 10); err != nil || len(got) != 1 {
		t.Fatalf("before replace: got %d err %v, want 1", len(got), err)
	}

	// Replace agent "t"'s memory for the source; the stale FTS row must go too.
	now := time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC)
	newMem := memory.Memory{ID: "memory:v2", Agent: "t", Kind: memory.MemoryKindSummary, Text: "날씨 정보", CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	newLink := memory.Link{SourceID: src.ID, MemoryID: newMem.ID, Kind: memory.LinkKindSourceIngest, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	if err := memories.ReplaceSourceMemories(ctx, src.ID, "t", []memory.Memory{newMem}, []memory.Link{newLink}); err != nil {
		t.Fatal(err)
	}

	if got, err := recaller.Search(ctx, "종목", "", 10); err != nil || len(got) != 0 {
		t.Fatalf("after replace, stale query: got %d err %v, want 0", len(got), err)
	}
	if got, err := recaller.Search(ctx, "날씨", "", 10); err != nil || len(got) != 1 {
		t.Fatalf("after replace, new query: got %d err %v, want 1", len(got), err)
	}
}

func TestEnsureFTSIndexedBackfillsExistingMemories(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := migrate.Apply(ctx, db, "unused"); err != nil {
		t.Fatal(err)
	}
	sources := source.NewStore(db)
	memories := memory.NewStore(db, spaceTokenizer{})
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	// Simulate a store populated before the index existed.
	if _, err := db.ExecContext(ctx, "DELETE FROM memories_fts"); err != nil {
		t.Fatal(err)
	}
	if got, err := memories.SearchMemories(ctx, "종목", "", 10); err != nil || len(got) != 0 {
		t.Fatalf("with empty index: got %d err %v, want 0", len(got), err)
	}

	if err := memories.EnsureFTSIndexed(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := memories.SearchMemories(ctx, "종목", "", 10); err != nil || len(got) != 1 {
		t.Fatalf("after backfill: got %d err %v, want 1", len(got), err)
	}
}

// TestSearchWithKoreanMorphologyMatchesInflectedText is the headline regression:
// the bare noun "종목" must recall text where it appears with an attached josa
// ("종목을"). A naive whitespace/trigram index cannot do this — only morphological
// tokenization splits "종목을" into "종목"+"을".
func TestSearchWithKoreanMorphologyMatchesInflectedText(t *testing.T) {
	ctx := context.Background()
	tok, err := tokenize.NewKorean()
	if err != nil {
		t.Fatal(err)
	}
	sources, memories, closeStores := openStoresWith(ctx, t, filepath.Join(t.TempDir(), "m.db"), tok)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:ko", "/work/ko"), "memory:ko", "코스피 종목을 분석했다")

	got, err := memory.NewRecaller(memories).Search(ctx, "종목", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("korean morphology recall = %d, want 1 (josa must be stripped)", len(got))
	}
}

func scopedSource(id source.SourceID, scope string) source.Source {
	return source.Source{
		ID:            id,
		Kind:          source.SourceKindCodexSession,
		URI:           "memory://test/" + string(id),
		ContentSHA256: "hash-" + string(id),
		Scope:         source.Scope{Kind: source.ScopeKindWorkspace, Value: scope},
		RecordedAt:    time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		MetadataJSON:  json.RawMessage(`{}`),
	}
}

func recordMemory(ctx context.Context, t *testing.T, sources *source.Store, memories *memory.Store, src source.Source, memID, text string) {
	t.Helper()
	if err := sources.RecordSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	mem := memory.Memory{ID: memory.MemoryID(memID), Agent: "t", Kind: memory.MemoryKindSummary, Text: text, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	link := memory.Link{SourceID: src.ID, MemoryID: mem.ID, Kind: memory.LinkKindSourceIngest, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
	if err := memories.ReplaceSourceMemories(ctx, src.ID, "t", []memory.Memory{mem}, []memory.Link{link}); err != nil {
		t.Fatal(err)
	}
}
