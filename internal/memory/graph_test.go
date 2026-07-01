package memories_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
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

// relateMemories records a Memory<->Memory Relation (ADR-0007) by inserting
// directly into memory_relations on the shared database file, standing in for an
// agent that authored a relates_to during ingest. Both memories must already
// exist so the foreign keys hold. It lets a graph test drive Relation edges and
// Relation degree without wiring the full ingest allowlist path.
func relateMemories(ctx context.Context, t *testing.T, path, from, to string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_relations
		(from_memory_id, to_memory_id, kind, created_at, metadata_json)
		VALUES (?, ?, 'relates', ?, '{}')`, from, to, now); err != nil {
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

// TestGraphSourceSizeIsFanOut proves a Source node's size is its fan-out — the
// number of distinct Memories that melted out of it — replacing the degenerate
// per-Memory Link fan-in metric (ADR-0008).
func TestGraphSourceSizeIsFanOut(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	sources, memories, closeStores := openStores(ctx, t, path)
	defer closeStores()

	// One prolific Source melts into three Memories (fan-out 3); a second Source
	// melts into one (fan-out 1). The agent id must differ per memory so the
	// re-link never deletes a prior memory of the same source.
	for i, mem := range []string{"memory:a", "memory:b", "memory:c"} {
		src := scopedSource("codex_session:hot", "/work/a")
		if err := sources.SaveSource(ctx, src, nil); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
		agent := "a" + string(rune('0'+i))
		m := memoriespkg.Memory{ID: memoriespkg.MemoryID(mem), Agent: agent, Kind: memoriespkg.MemoryKindSummary, Text: mem, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
		link := memoriespkg.Link{SourceID: src.ID, MemoryID: m.ID, Kind: memoriespkg.LinkKindSourceIngest, CreatedAt: now, MetadataJSON: json.RawMessage(`{}`)}
		if err := memories.ReplaceSourceMemories(ctx, src.ID, agent, []memoriespkg.Memory{m}, []memoriespkg.Link{link}, nil); err != nil {
			t.Fatal(err)
		}
	}
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:cold", "/work/a"), "memory:d", "d memory")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}

	srcNodes, _ := nodesByKind(graph.Nodes)
	fanOut := map[string]int{}
	for _, n := range srcNodes {
		fanOut[n.ID] = n.Size
	}
	if fanOut["codex_session:hot"] != 3 {
		t.Fatalf("codex_session:hot fan-out = %d, want 3 (three melted memories)", fanOut["codex_session:hot"])
	}
	if fanOut["codex_session:cold"] != 1 {
		t.Fatalf("codex_session:cold fan-out = %d, want 1", fanOut["codex_session:cold"])
	}
}

// TestGraphMemorySizeIsRelationDegree proves a Memory node's size is its Relation
// degree — how many other Memories it connects to via Memory<->Memory Relations,
// counting both directions — replacing the degenerate Link fan-in metric.
func TestGraphMemorySizeIsRelationDegree(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	sources, memories, closeStores := openStores(ctx, t, path)
	defer closeStores()

	// A hub Memory relates to two others; one leaf relates only to the hub.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:hub", "hub")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s2", "/work/a"), "memory:leafA", "leafA")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s3", "/work/a"), "memory:leafB", "leafB")
	// hub -> leafA, leafB -> hub. hub has degree 2, each leaf degree 1.
	relateMemories(ctx, t, path, "memory:hub", "memory:leafA")
	relateMemories(ctx, t, path, "memory:leafB", "memory:hub")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}

	_, memNodes := nodesByKind(graph.Nodes)
	degree := map[string]int{}
	for _, n := range memNodes {
		degree[n.ID] = n.Size
	}
	if degree["memory:hub"] != 2 {
		t.Fatalf("memory:hub relation degree = %d, want 2 (both directions counted)", degree["memory:hub"])
	}
	if degree["memory:leafA"] != 1 || degree["memory:leafB"] != 1 {
		t.Fatalf("leaf degrees = %d/%d, want 1/1", degree["memory:leafA"], degree["memory:leafB"])
	}
}

// TestGraphReturnsRelationEdges proves the scoped graph returns Memory<->Memory
// Relation edges alongside Link edges, so the /graph page can render them
// distinctly. Relation edges only connect two in-scope, kept Memory nodes.
func TestGraphReturnsRelationEdges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	sources, memories, closeStores := openStores(ctx, t, path)
	defer closeStores()

	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:a", "a")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s2", "/work/a"), "memory:b", "b")
	// A memory in another scope must never appear as a Relation endpoint here.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:x1", "/work/b"), "memory:x", "x")
	relateMemories(ctx, t, path, "memory:a", "memory:b")
	relateMemories(ctx, t, path, "memory:a", "memory:x") // cross-scope: must be excluded

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Relations) != 1 {
		t.Fatalf("relation edges = %d, want 1 (a->b in scope; a->x is cross-scope)", len(graph.Relations))
	}
	rel := graph.Relations[0]
	if rel.FromMemoryID != "memory:a" || rel.ToMemoryID != "memory:b" {
		t.Fatalf("relation edge = %+v, want memory:a -> memory:b", rel)
	}
}

// TestGraphNoRelationsDegradesCleanly proves a scope with zero Relations still
// returns the provenance graph with a non-nil empty Relations slice, so the view
// degrades to provenance-only with no regression.
func TestGraphNoRelationsDegradesCleanly(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:a", "a")

	graph, err := memories.Graph(ctx, "/work/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Relations == nil {
		t.Fatal("relations must be a non-nil empty slice when the scope has no relations")
	}
	if len(graph.Relations) != 0 {
		t.Fatalf("relations = %d, want 0 for a relation-free scope", len(graph.Relations))
	}
	if len(graph.Nodes) == 0 || len(graph.Edges) == 0 {
		t.Fatalf("provenance graph must still render: %d nodes / %d edges", len(graph.Nodes), len(graph.Edges))
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
	if empty.Nodes == nil || empty.Edges == nil || empty.Relations == nil {
		t.Fatalf("empty-store nodes/edges/relations must be non-nil empty slices, got %#v", empty)
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

// TestMemoryNeighborhoodIncludesConnectedMemories proves the drilldown expands
// into connected Memory neighbors (via Relations), not just derived Sources, and
// returns the Relation edges to them.
func TestMemoryNeighborhoodIncludesConnectedMemories(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	sources, memories, closeStores := openStores(ctx, t, path)
	defer closeStores()

	// center relates to two other memories (one via from, one via to); it also
	// has a derived source.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s1", "/work/a"), "memory:center", "center")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s2", "/work/a"), "memory:nbrA", "nbrA")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:s3", "/work/b"), "memory:nbrB", "nbrB")
	relateMemories(ctx, t, path, "memory:center", "memory:nbrA")
	relateMemories(ctx, t, path, "memory:nbrB", "memory:center")

	graph, found, err := memories.MemoryNeighborhood(ctx, "memory:center")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("memory:center not found, want found")
	}
	_, memNodes := nodesByKind(graph.Nodes)
	ids := map[string]bool{}
	for _, n := range memNodes {
		ids[n.ID] = true
	}
	if !ids["memory:center"] || !ids["memory:nbrA"] || !ids["memory:nbrB"] {
		t.Fatalf("neighborhood memory nodes = %v, want center + both connected neighbors", ids)
	}
	if len(graph.Relations) != 2 {
		t.Fatalf("neighborhood relation edges = %d, want 2 (both directions of relation)", len(graph.Relations))
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
