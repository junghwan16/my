package memories_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// meltSourceIntoMemory records a Link (Source->Memory) by saving the source and
// upserting the given memory linked to it. Calling it repeatedly with the same
// memory id but different sources builds a Memory with a Link fan-in greater than
// one, so a test can assert per-Memory fan-in and the aggregate average. The
// agent is fixed so re-linking never deletes a prior source's link.
func meltSourceIntoMemory(ctx context.Context, t *testing.T, sources *sourcespkg.Store, memories *memoriespkg.Store, src sourcespkg.Source, memID, text string) {
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

// nodesByKind splits graph nodes into Source and Memory nodes so a test can
// assert on each family independently.
func nodesByKind(nodes []memoriespkg.GraphNode) (sources, mems []memoriespkg.GraphNode) {
	for _, n := range nodes {
		switch n.Kind {
		case memoriespkg.GraphNodeSource:
			sources = append(sources, n)
		case memoriespkg.GraphNodeMemory:
			mems = append(mems, n)
		}
	}
	return sources, mems
}

// TestGraphReturnsScopedNodesAndLinkEdges is the #18 happy path: the graph for a
// scope holds that scope's Source and Memory nodes and the Link edges between
// them, and nothing from another scope leaks in.
func TestGraphReturnsScopedNodesAndLinkEdges(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// Scope /work/a: two sources melt into memory:a (fan-in 2); scope /work/b:
	// one source into memory:b.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:a1", "/work/a"), "memory:a", "a memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:a2", "/work/a"), "memory:a", "a memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:b1", "/work/b"), "memory:b", "b memory")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}

	srcNodes, memNodes := nodesByKind(graph.Nodes)
	if len(memNodes) != 1 || memNodes[0].ID != "memory:a" {
		t.Fatalf("memory nodes = %#v, want single memory:a (memory:b is out of scope)", memNodes)
	}
	if len(srcNodes) != 2 {
		t.Fatalf("source nodes = %d, want 2 (the two /work/a sources)", len(srcNodes))
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("link edges = %d, want 2 (both /work/a sources -> memory:a)", len(graph.Edges))
	}
	for _, e := range graph.Edges {
		if e.MemoryID != "memory:a" {
			t.Fatalf("edge target = %q, want memory:a", e.MemoryID)
		}
	}
}

// TestGraphMemoryFanInCountsMeltedSources proves a Memory node's fan-in is the
// number of Sources that melted into it (its Link fan-in), which the UI turns
// into node size/badge.
func TestGraphMemoryFanInCountsMeltedSources(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:hot", "hot memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s2", "/work/a"), "memory:hot", "hot memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s3", "/work/a"), "memory:hot", "hot memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:c1", "/work/a"), "memory:cold", "cold memory")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}

	_, memNodes := nodesByKind(graph.Nodes)
	fanIn := map[string]int{}
	for _, n := range memNodes {
		fanIn[n.ID] = n.FanIn
	}
	if fanIn["memory:hot"] != 3 {
		t.Fatalf("memory:hot fan-in = %d, want 3 (three melted sources)", fanIn["memory:hot"])
	}
	if fanIn["memory:cold"] != 1 {
		t.Fatalf("memory:cold fan-in = %d, want 1", fanIn["memory:cold"])
	}
}

// TestGraphStatsAggregatesWholeScope proves the aggregate panel reports the total
// Sources, total Memories, and average Sources per Memory over the whole scope.
func TestGraphStatsAggregatesWholeScope(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// /work/a: 3 sources, 2 memories, 3 links (hot<-s1,s2 ; cold<-s3) => avg 1.5.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:hot", "hot")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s2", "/work/a"), "memory:hot", "hot")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s3", "/work/a"), "memory:cold", "cold")
	// Another scope must not affect /work/a stats.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:x1", "/work/b"), "memory:x", "x")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Stats.Sources != 3 {
		t.Fatalf("stats sources = %d, want 3", graph.Stats.Sources)
	}
	if graph.Stats.Memories != 2 {
		t.Fatalf("stats memories = %d, want 2", graph.Stats.Memories)
	}
	if graph.Stats.AvgSourcesPerMem != 1.5 {
		t.Fatalf("stats avg sources/memory = %v, want 1.5", graph.Stats.AvgSourcesPerMem)
	}
}

// TestGraphCapTruncatesButStatsStayWhole proves the node cap truncates the
// returned nodes while the aggregate panel still reports the whole scope, so the
// overview stays renderable and the totals stay honest.
func TestGraphCapTruncatesButStatsStayWhole(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// Six single-source memories in one scope.
	for i := 0; i < 6; i++ {
		id := sourcespkg.SourceID("codex_session:s" + string(rune('0'+i)))
		mem := "memory:m" + string(rune('0'+i))
		meltSourceIntoMemory(ctx, t, sources, memories, scopedSource(id, "/work/a"), mem, "m")
	}

	// A tiny cap forces truncation: with cap=2 only ~1 memory node fits.
	graph, err := memories.Graph(ctx, "/work/a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Truncated {
		t.Fatalf("truncated = false, want true (6 memories over a cap of 2)")
	}
	if len(graph.Nodes) > 2 {
		t.Fatalf("returned %d nodes, want <= cap 2", len(graph.Nodes))
	}
	// The aggregate panel is cap-independent: it must report all 6.
	if graph.Stats.Memories != 6 || graph.Stats.Sources != 6 {
		t.Fatalf("stats = %d sources / %d memories, want 6 / 6 (cap must not affect stats)", graph.Stats.Sources, graph.Stats.Memories)
	}
}

// TestGraphEmptyStoreAndScopeReturnCleanEmpty proves an empty store and an empty
// scope both yield a clean empty graph (non-nil empty slices, zeroed stats, not
// truncated), so the JSON contract stays stable.
func TestGraphEmptyStoreAndScopeReturnCleanEmpty(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	empty, err := memories.Graph(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Nodes == nil || empty.Edges == nil {
		t.Fatalf("empty-store nodes/edges must be non-nil empty slices, got %#v", empty)
	}
	if len(empty.Nodes) != 0 || len(empty.Edges) != 0 || empty.Truncated {
		t.Fatalf("empty store graph = %#v, want no nodes/edges and not truncated", empty)
	}
	if empty.Stats.Sources != 0 || empty.Stats.Memories != 0 || empty.Stats.AvgSourcesPerMem != 0 {
		t.Fatalf("empty store stats = %#v, want zeroed", empty.Stats)
	}

	// Populate one scope, then query an unrelated scope: also a clean empty.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:a1", "/work/a"), "memory:a", "a")
	emptyScope, err := memories.Graph(ctx, "/nonexistent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyScope.Nodes) != 0 || len(emptyScope.Edges) != 0 || emptyScope.Stats.Memories != 0 {
		t.Fatalf("empty scope graph = %#v, want clean empty", emptyScope)
	}
}

// TestMemoryNeighborhoodExpandsProvenance proves the click-to-expand drilldown
// returns a Memory plus every Source that melted into it, unrestricted by scope.
func TestMemoryNeighborhoodExpandsProvenance(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// memory:multi is melted from sources in two different scopes; the drilldown
	// must show both, unlike a scoped overview.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:p1", "/work/a"), "memory:multi", "multi")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:p2", "/work/b"), "memory:multi", "multi")

	graph, found, err := memories.MemoryNeighborhood(ctx, "memory:multi")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("memory:multi not found, want found")
	}
	srcNodes, memNodes := nodesByKind(graph.Nodes)
	if len(memNodes) != 1 || memNodes[0].ID != "memory:multi" {
		t.Fatalf("neighborhood memory nodes = %#v, want single memory:multi", memNodes)
	}
	if len(srcNodes) != 2 {
		t.Fatalf("neighborhood source nodes = %d, want 2 (both scopes' sources)", len(srcNodes))
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("neighborhood edges = %d, want 2 Link edges", len(graph.Edges))
	}
}

// TestMemoryNeighborhoodUnknownIDNotFound proves an unknown Memory id reports
// found=false with no error, so the UI renders a clean miss.
func TestMemoryNeighborhoodUnknownIDNotFound(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	_, found, err := memories.MemoryNeighborhood(ctx, "memory:ghost")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("found = true for unknown id, want false")
	}
}
