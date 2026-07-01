package memory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/junghwan16/my/internal/memory"
)

// indexOf returns the position of memID in the recollection list, or -1.
func indexOf(recs []memory.Recollection, memID memory.MemoryID) int {
	for i, r := range recs {
		if r.MemoryID == memID {
			return i
		}
	}
	return -1
}

// TestHybridFusionSurfacesBothRankersTops crafts a set where one memory is #1
// lexically but weak semantically, and another is #1 semantically but weak
// lexically. RRF fusion must lift BOTH to the top of the result, above a
// distractor that is only mid-ranked in each engine — proving fusion beats
// either ranker alone.
func TestHybridFusionSurfacesBothRankersTops(t *testing.T) {
	ctx := context.Background()
	// Query embeds near the "semantic" memory's vector; the "lexical" memory's
	// vector is orthogonal, and the distractor's is in between.
	emb := fakeEmbedder{vectors: map[string][]float32{
		"lexical winner alpha alpha alpha": {0, 0, 1}, // far from query
		"unrelated words here":             {1, 0, 0}, // semantic winner, no query term
		"some middling filler content":     {0.7, 0, 0.7},
		"alpha":                            {1, 0, 0}, // query: matches lex, embeds near sem
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	// memory:lex — top lexical hit ("alpha" repeated), orthogonal semantically.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:lex", "/w/lex"),
		"memory:lex", "lexical winner alpha alpha alpha")
	// memory:sem — top semantic hit, but no lexical overlap with the query term.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:sem", "/w/sem"),
		"memory:sem", "unrelated words here")
	// memory:mid — a distractor: mid semantic, no lexical match. Fusion should
	// keep it below the two rankers' respective champions.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:mid", "/w/mid"),
		"memory:mid", "some middling filler content")

	got, err := memory.NewRecaller(memories).Recollect(ctx, "alpha", "", 10)
	if err != nil {
		t.Fatal(err)
	}

	lexPos := indexOf(got, "memory:lex")
	semPos := indexOf(got, "memory:sem")
	midPos := indexOf(got, "memory:mid")

	if lexPos < 0 || semPos < 0 {
		t.Fatalf("both champions must appear: lexPos=%d semPos=%d (got %+v)", lexPos, semPos, got)
	}
	// Both engine champions outrank the mid distractor: fusion beats either alone.
	if midPos >= 0 && (lexPos > midPos || semPos > midPos) {
		t.Fatalf("fused order should place lex(%d) and sem(%d) above mid(%d)", lexPos, semPos, midPos)
	}
	// The two champions occupy the top two slots.
	if lexPos > 1 || semPos > 1 {
		t.Fatalf("fusion did not lift both champions to the top two: lexPos=%d semPos=%d", lexPos, semPos)
	}
}

// TestHybridWithoutEmbedderEqualsLexical proves that with no embedder attached,
// hybrid recall (via Recollect) returns exactly the lexical ranking — no error,
// no semantic contribution — so recall degrades gracefully offline.
func TestHybridWithoutEmbedderEqualsLexical(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/w/a"), "memory:a", "종목 분석 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/w/b"), "memory:b", "종목 추천 종목 종목")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:c", "/w/c"), "memory:c", "오늘 날씨 정보")

	recaller := memory.NewRecaller(memories)

	hybrid, err := recaller.Recollect(ctx, "종목", "", 10)
	if err != nil {
		t.Fatalf("hybrid recall without embedder errored: %v", err)
	}

	lexical, err := recaller.Search(ctx, "종목", "", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hybrid) != len(lexical) {
		t.Fatalf("hybrid len %d != lexical len %d", len(hybrid), len(lexical))
	}
	for i := range lexical {
		if hybrid[i].MemoryID != lexical[i].ID {
			t.Fatalf("position %d: hybrid=%q lexical=%q; hybrid must equal lexical without embedder",
				i, hybrid[i].MemoryID, lexical[i].ID)
		}
	}
	// The non-matching memory never appears.
	if indexOf(hybrid, "memory:c") >= 0 {
		t.Fatalf("non-matching memory:c leaked into hybrid recall")
	}
}

// TestHybridDeterministicOnEqualScores proves that memories with equal fused
// scores come back in a deterministic order. Two memories match the same single
// lexical term and share an identical semantic vector, so they tie on both
// rankers and thus on the fused score; the tie must break by MemoryID (they
// share a CreatedAt), and repeated calls must return the same order.
func TestHybridDeterministicOnEqualScores(t *testing.T) {
	ctx := context.Background()
	emb := fakeEmbedder{vectors: map[string][]float32{
		"beta one": {1, 0, 0},
		"beta two": {1, 0, 0}, // identical vector => identical semantic rank basis
		"beta":     {1, 0, 0}, // query embeds identically => cosine 1, tie above the floor
	}}
	sources, memories, closeStores := openSemanticStores(ctx, t, emb)
	defer closeStores()

	// Same single matching term "beta", identical vectors, identical CreatedAt.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:1", "/w/1"), "memory:1", "beta one")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:2", "/w/2"), "memory:2", "beta two")

	recaller := memory.NewRecaller(memories)

	var first []memory.Recollection
	for i := range 5 {
		got, err := recaller.Recollect(ctx, "beta", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("run %d: got %d results, want 2", i, len(got))
		}
		if first == nil {
			first = got
			continue
		}
		for j := range got {
			if got[j].MemoryID != first[j].MemoryID {
				t.Fatalf("non-deterministic order at run %d pos %d: %q vs %q",
					i, j, got[j].MemoryID, first[j].MemoryID)
			}
		}
	}
	// Deterministic tie-break is by MemoryID ascending (shared CreatedAt).
	if first[0].MemoryID != "memory:1" || first[1].MemoryID != "memory:2" {
		t.Fatalf("tie-break order = [%q %q], want [memory:1 memory:2]", first[0].MemoryID, first[1].MemoryID)
	}
}
