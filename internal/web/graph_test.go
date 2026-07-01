package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/web"
)

// graphBody mirrors the /api/graph JSON contract: Source and Memory nodes, Link
// edges, the aggregate panel, and the truncation flag.
type graphBody struct {
	Nodes []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Label string `json:"label"`
		FanIn int    `json:"fan_in"`
		Scope struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"scope"`
	} `json:"nodes"`
	Edges []struct {
		SourceID string `json:"source_id"`
		MemoryID string `json:"memory_id"`
	} `json:"edges"`
	Stats struct {
		Sources          int     `json:"sources"`
		Memories         int     `json:"memories"`
		AvgSourcesPerMem float64 `json:"avg_sources_per_memory"`
	} `json:"stats"`
	Truncated bool `json:"truncated"`
}

// meltSourceIntoMemory records a Link (Source->Memory), reusing recordMemory's
// pattern but letting several sources melt into the same memory so a test drives
// fan-in and the aggregate average through the HTTP surface. The agent is fixed
// so re-linking a memory never deletes a prior source's link.
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

func TestGraphReturnsScopedNodesEdgesAndStats(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// /work/a: two sources melt into memory:a (fan-in 2). /work/b is a distraction.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:a1", "/work/a"), "memory:a", "a memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:a2", "/work/a"), "memory:a", "a memory")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:b1", "/work/b"), "memory:b", "b memory")

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getGraph(ctx, t, server.URL+"/api/graph?scope=/work/a")

	var memNodes, srcNodes int
	var memoryFanIn int
	for _, n := range body.Nodes {
		switch n.Kind {
		case "memory":
			memNodes++
			if n.ID == "memory:a" {
				memoryFanIn = n.FanIn
			}
		case "source":
			srcNodes++
		}
	}
	if memNodes != 1 {
		t.Fatalf("memory nodes = %d, want 1 (memory:b is out of scope)", memNodes)
	}
	if srcNodes != 2 {
		t.Fatalf("source nodes = %d, want 2", srcNodes)
	}
	if memoryFanIn != 2 {
		t.Fatalf("memory:a fan-in = %d, want 2", memoryFanIn)
	}
	if len(body.Edges) != 2 {
		t.Fatalf("link edges = %d, want 2", len(body.Edges))
	}
	if body.Stats.Sources != 2 || body.Stats.Memories != 1 || body.Stats.AvgSourcesPerMem != 2 {
		t.Fatalf("stats = %#v, want 2 sources / 1 memory / avg 2", body.Stats)
	}
}

func TestGraphEmptyStoreReturnsCleanEmpty(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getGraph(ctx, t, server.URL+"/api/graph?scope=")
	if len(body.Nodes) != 0 || len(body.Edges) != 0 {
		t.Fatalf("empty store graph = %d nodes / %d edges, want 0 / 0", len(body.Nodes), len(body.Edges))
	}
	if body.Stats.Sources != 0 || body.Stats.Memories != 0 || body.Stats.AvgSourcesPerMem != 0 {
		t.Fatalf("empty store stats = %#v, want zeroed", body.Stats)
	}
	if body.Truncated {
		t.Fatal("empty store graph must not be truncated")
	}
}

func TestGraphCapTruncatesOverviewButNotStats(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	for i := 0; i < 6; i++ {
		id := sourcespkg.SourceID("codex_session:s" + string(rune('0'+i)))
		mem := "memory:m" + string(rune('0'+i))
		meltSourceIntoMemory(ctx, t, sources, memories, scopedSource(id, "/work/a"), mem, "m")
	}

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getGraph(ctx, t, server.URL+"/api/graph?scope=/work/a&cap=2")
	if !body.Truncated {
		t.Fatal("truncated = false, want true (6 memories over cap 2)")
	}
	if len(body.Nodes) > 2 {
		t.Fatalf("returned %d nodes, want <= cap 2", len(body.Nodes))
	}
	if body.Stats.Memories != 6 || body.Stats.Sources != 6 {
		t.Fatalf("stats = %#v, want 6 sources / 6 memories regardless of cap", body.Stats)
	}
}

func TestGraphRejectsBadCap(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/graph?cap=abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-numeric cap", resp.StatusCode)
	}
}

func TestMemoryNeighborhoodExpandsAcrossScopes(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// memory:multi is melted from sources in two scopes; the drilldown must show
	// both, unlike a scoped overview.
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:p1", "/work/a"), "memory:multi", "multi")
	meltSourceIntoMemory(ctx, t, sources, memories, scopedSource("codex_session:p2", "/work/b"), "memory:multi", "multi")

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getGraph(ctx, t, server.URL+"/api/graph/memory?id=memory:multi")

	var memNodes, srcNodes int
	for _, n := range body.Nodes {
		switch n.Kind {
		case "memory":
			memNodes++
		case "source":
			srcNodes++
		}
	}
	if memNodes != 1 {
		t.Fatalf("neighborhood memory nodes = %d, want 1", memNodes)
	}
	if srcNodes != 2 {
		t.Fatalf("neighborhood source nodes = %d, want 2 (both scopes)", srcNodes)
	}
	if len(body.Edges) != 2 {
		t.Fatalf("neighborhood edges = %d, want 2", len(body.Edges))
	}
}

func TestMemoryNeighborhoodUnknownIDIs404(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/graph/memory?id=memory:ghost", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown memory", resp.StatusCode)
	}
}

func TestMemoryNeighborhoodMissingIDIs400(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/graph/memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when id is missing", resp.StatusCode)
	}
}

func TestServesGraphPage(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	for _, path := range []string{"/graph", "/vendor/cytoscape.min.js"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			closeResp(t, resp)
			t.Fatalf("GET %s status = %d, want 200 (page + vendored library must serve offline)", path, resp.StatusCode)
		}
		closeResp(t, resp)
	}
}

// getGraph fetches a graph endpoint at url and decodes the JSON contract. It
// fails the test on any transport, status, or decode error.
func getGraph(ctx context.Context, t *testing.T, url string) graphBody {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, resp.StatusCode)
	}
	var body graphBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	return body
}
