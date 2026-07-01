package memory_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/migrate"
	"github.com/junghwan16/my/internal/source"
	"github.com/junghwan16/my/internal/storage"
)

// fakeEmbedder returns deterministic vectors keyed by exact text, so tests
// control cosine geometry with no Ollama and no network. Unknown text maps to a
// fixed neutral vector, and an optional failText makes Embed fail once to prove
// graceful skips.
type fakeEmbedder struct {
	model    string
	vectors  map[string][]float32
	fallback []float32
	failText string
}

func (f fakeEmbedder) Model() string {
	if f.model == "" {
		return "fake"
	}
	return f.model
}

func (f fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if f.failText != "" && text == f.failText {
		return nil, errFakeEmbed
	}
	if v, ok := f.vectors[text]; ok {
		return v, nil
	}
	if f.fallback != nil {
		return f.fallback, nil
	}
	return []float32{0, 0, 1}, nil
}

var errFakeEmbed = errors.New("fake embed failure")

// openSemanticStores opens stores with an embedder attached for semantic tests.
func openSemanticStores(ctx context.Context, t *testing.T, emb memory.Embedder) (*source.Store, *memory.Store, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	closeStore := func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err = migrate.Apply(ctx, db, "unused"); err != nil {
		closeStore()
		t.Fatal(err)
	}
	memories := memory.NewStore(db, spaceTokenizer{}).WithEmbedder(emb)
	return source.NewStore(db), memories, closeStore
}

// TestSearchSemanticRanksByCosine proves the query embedding closest to memory
// A's vector ranks A first. Both memories sit above the similarity floor, so the
// assertion isolates ordering, not filtering.
func TestSearchSemanticRanksByCosine(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{vectors: map[string][]float32{
		"apple text":       {1, 0, 0},
		"banana text":      {0, 1, 0},
		"query near apple": {0.9, 0.6, 0},
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:apple", "apple text")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:banana", "banana text")

	got, err := memory.NewRecaller(memories).SearchSemantic(ctx, "query near apple", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("semantic results = %d, want 2", len(got))
	}
	if got[0].ID != "memory:apple" {
		t.Fatalf("top result = %q, want memory:apple (closest cosine)", got[0].ID)
	}
}

// TestSearchSemanticDropsBelowSimilarityFloor proves the cosine floor excludes
// out-of-domain memories: an off-topic query (near-orthogonal to every stored
// vector, low cosine) returns nothing, while an on-topic query (high cosine to a
// stored vector) still returns that memory. It asserts threshold behavior
// (below floor excluded, above floor included), not exact cosine numbers.
func TestSearchSemanticDropsBelowSimilarityFloor(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{vectors: map[string][]float32{
		// Two dev memories share the same direction.
		"schema migration notes": {1, 0, 0},
		"query index tuning":     {1, 0, 0},
		// On-topic query: aligned with the stored vectors (cosine 1.0, above floor).
		"on topic query": {0.95, 0.05, 0},
		// Off-topic query: near-orthogonal to every stored vector (cosine ~0, below floor).
		"off topic query": {0, 0, 1},
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "schema migration notes")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/a"), "memory:b", "query index tuning")

	recaller := memory.NewRecaller(memories)

	onTopic, err := recaller.SearchSemantic(ctx, "on topic query", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(onTopic) != 2 {
		t.Fatalf("on-topic semantic results = %d, want 2 (both above floor)", len(onTopic))
	}

	offTopic, err := recaller.SearchSemantic(ctx, "off topic query", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(offTopic) != 0 {
		t.Fatalf("off-topic semantic results = %d, want 0 (all below cosine floor)", len(offTopic))
	}
}

// TestSearchSemanticFloorConfigurable proves WithMinSimilarity tunes the floor:
// lowering it below a memory's cosine lets that otherwise-excluded memory back
// into the results.
func TestSearchSemanticFloorConfigurable(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	if err = migrate.Apply(ctx, db, "unused"); err != nil {
		t.Fatal(err)
	}
	sources := source.NewStore(db)

	emb := fakeEmbedder{vectors: map[string][]float32{
		// Stored vector is near-orthogonal to the query: cosine well below the
		// default floor, so the default store excludes it.
		"weakly related memory": {0, 1, 0},
		"query":                 {1, 0.2, 0},
	}}
	memories := memory.NewStore(db, spaceTokenizer{}).WithEmbedder(emb)
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "weakly related memory")

	// Default floor (0.5) excludes the weakly related memory.
	got, err := memory.NewRecaller(memories).SearchSemantic(ctx, "query", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("default-floor results = %d, want 0 (below floor)", len(got))
	}

	// A floor of -1 admits every candidate, so the same memory is now returned.
	loose := memory.NewStore(db, spaceTokenizer{}).WithEmbedder(emb).WithMinSimilarity(-1)
	got, err = memory.NewRecaller(loose).SearchSemantic(ctx, "query", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "memory:a" {
		t.Fatalf("loose-floor results = %#v, want single memory:a", got)
	}
}

// TestSearchSemanticRespectsScopeAndLimit proves scope filters candidates and
// limit caps results, like SearchMemories.
func TestSearchSemanticRespectsScopeAndLimit(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{vectors: map[string][]float32{
		"a": {1, 0, 0}, "b": {0.99, 0.01, 0}, "c": {0.98, 0.02, 0},
		"query": {1, 0, 0},
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "a")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/a"), "memory:b", "b")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:c", "/work/other"), "memory:c", "c")

	recaller := memory.NewRecaller(memories)

	scoped, err := recaller.SearchSemantic(ctx, "query", "/work/a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 {
		t.Fatalf("scoped semantic results = %d, want 2 (only /work/a)", len(scoped))
	}
	for _, m := range scoped {
		if m.ID == "memory:c" {
			t.Fatalf("out-of-scope memory:c leaked into scoped results")
		}
	}

	limited, err := recaller.SearchSemantic(ctx, "query", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited semantic results = %d, want 1", len(limited))
	}
}

// TestSearchSemanticWritesVectorsOnRecord proves memories are embedded on write
// (a vector row exists so semantic recall finds the memory immediately).
func TestSearchSemanticWritesVectorsOnRecord(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{vectors: map[string][]float32{
		"hello": {1, 0, 0}, "query": {1, 0, 0},
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "hello")

	got, err := memory.NewRecaller(memories).SearchSemantic(ctx, "query", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "memory:a" {
		t.Fatalf("semantic recall after record = %#v, want single memory:a", got)
	}
}

// TestEnsureVectorsIndexedBackfills proves an embedder attached after memories
// already exist backfills their vectors on EnsureVectorsIndexed.
func TestEnsureVectorsIndexedBackfills(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	if err = migrate.Apply(ctx, db, "unused"); err != nil {
		t.Fatal(err)
	}
	sources := source.NewStore(db)

	// Record with no embedder: no vectors written.
	plain := memory.NewStore(db, spaceTokenizer{})
	recordMemory(ctx, t, sources, plain, scopedSource("codex_session:a", "/work/a"), "memory:a", "hello")

	emb := fakeEmbedder{vectors: map[string][]float32{"hello": {1, 0, 0}, "query": {1, 0, 0}}}
	withEmb := memory.NewStore(db, spaceTokenizer{}).WithEmbedder(emb)

	// Before backfill: no vectors, so semantic recall is empty.
	before, err := withEmb.SearchSemantic(ctx, "query", "", 10)
	if err != nil || len(before) != 0 {
		t.Fatalf("before backfill: got %d err %v, want 0", len(before), err)
	}

	if err = withEmb.EnsureVectorsIndexed(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := withEmb.SearchSemantic(ctx, "query", "", 10)
	if err != nil || len(after) != 1 {
		t.Fatalf("after backfill: got %d err %v, want 1", len(after), err)
	}
}

// TestSemanticFallbackWhenNoEmbedder proves that with no embedder, semantic
// recall returns nothing and no error, and lexical recall still works.
func TestSemanticFallbackWhenNoEmbedder(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	recaller := memory.NewRecaller(memories)

	semantic, err := recaller.SearchSemantic(ctx, "종목", "", 10)
	if err != nil {
		t.Fatalf("semantic recall without embedder errored: %v", err)
	}
	if len(semantic) != 0 {
		t.Fatalf("semantic results without embedder = %d, want 0", len(semantic))
	}

	// Lexical recall is unaffected.
	lexical, err := recaller.Search(ctx, "종목", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical) != 1 || lexical[0].ID != "memory:a" {
		t.Fatalf("lexical recall = %#v, want single memory:a", lexical)
	}

	// EnsureVectorsIndexed is a no-op with no embedder.
	if err := memories.EnsureVectorsIndexed(ctx); err != nil {
		t.Fatalf("EnsureVectorsIndexed without embedder errored: %v", err)
	}
}

// TestEmbedFailureSkipsVectorButKeepsMemory proves a failing embed on write
// stores the memory (and lexical index) but no vector, so the write never
// fails and lexical recall stays intact.
func TestEmbedFailureSkipsVectorButKeepsMemory(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{
		vectors:  map[string][]float32{"query": {1, 0, 0}},
		failText: "종목 분석",
	}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	// Memory persisted despite the embed failure.
	recalled, err := memory.NewRecaller(memories).Recall(ctx, "codex_session:a")
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 {
		t.Fatalf("source memories = %d, want 1 (memory kept despite embed failure)", len(recalled))
	}

	// No vector, so semantic recall finds nothing.
	if got, err := memories.SearchSemantic(ctx, "query", "", 10); err != nil || len(got) != 0 {
		t.Fatalf("semantic after embed failure: got %d err %v, want 0", len(got), err)
	}
}
