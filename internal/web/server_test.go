package web_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
	"github.com/junghwan16/gieok/internal/tokenize"
	"github.com/junghwan16/gieok/internal/web"
)

// recallBody mirrors the /api/recall JSON contract: a "memories" array of ranked
// results, each carrying the Source context it derives from.
type recallBody struct {
	Memories []struct {
		MemoryID string `json:"memory_id"`
		Agent    string `json:"agent"`
		Kind     string `json:"kind"`
		Text     string `json:"text"`
		Sources  []struct {
			ID    string `json:"id"`
			URI   string `json:"uri"`
			Scope struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"scope"`
		} `json:"sources"`
	} `json:"memories"`
}

func TestRecallReturnsRankedMemoryAsJSON(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "오늘 날씨 정보")

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getRecall(ctx, t, server.URL+"/api/recall?query=종목")

	if len(body.Memories) != 1 {
		t.Fatalf("recalled %d memories, want 1 (only the matching one)", len(body.Memories))
	}
	got := body.Memories[0]
	if got.MemoryID != "memory:a" {
		t.Fatalf("memory id = %q, want memory:a", got.MemoryID)
	}
	if got.Text != "코스피 종목 분석 리포트" {
		t.Fatalf("text = %q, want the saved memory text", got.Text)
	}
	if got.Agent != "t" || got.Kind != string(memoriespkg.MemoryKindSummary) {
		t.Fatalf("agent/kind = %q/%q, want t/summary", got.Agent, got.Kind)
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

func TestRecallEmptyQueryReturnsRecentMemory(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "오늘 날씨 정보")

	// Empty default scope so the recent path spans every Scope.
	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getRecall(ctx, t, server.URL+"/api/recall")

	if len(body.Memories) != 2 {
		t.Fatalf("empty query returned %d memories, want 2 recent memories", len(body.Memories))
	}
}

func TestRecallHonorsScopeParameter(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	scoped := getRecall(ctx, t, server.URL+"/api/recall?query=종목&scope=/work/a")
	if len(scoped.Memories) != 1 || scoped.Memories[0].MemoryID != "memory:a" {
		t.Fatalf("scoped recall = %#v, want single memory:a", scoped.Memories)
	}

	all := getRecall(ctx, t, server.URL+"/api/recall?query=종목&scope=")
	if len(all.Memories) != 2 {
		t.Fatalf("unscoped recall = %d, want 2", len(all.Memories))
	}
}

func TestRecallUsesDefaultScopeWhenParameterAbsent(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")

	// Server default scope pins recall to /work/a when the request omits scope.
	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "/work/a").Handler())
	defer server.Close()

	body := getRecall(ctx, t, server.URL+"/api/recall?query=종목")
	if len(body.Memories) != 1 || body.Memories[0].MemoryID != "memory:a" {
		t.Fatalf("default-scope recall = %#v, want single memory:a", body.Memories)
	}
}

func TestRecallRejectsBadLimit(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/recall?limit=abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-numeric limit", resp.StatusCode)
	}
}

// scopesBody mirrors the /api/scopes JSON contract: a "scopes" array of the
// distinct Scopes the store holds, each with its kind and value.
type scopesBody struct {
	Scopes []struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"scopes"`
}

func TestScopesReturnsDistinctStoreScopes(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	// Two sources share /work/a (must dedupe) and one is in /work/b.
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a1", "/work/a"), "memory:a1", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a2", "/work/a"), "memory:a2", "종목 리포트")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "종목 추천")

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getScopes(ctx, t, server.URL+"/api/scopes")

	if len(body.Scopes) != 2 {
		t.Fatalf("scopes = %d (%#v), want 2 distinct", len(body.Scopes), body.Scopes)
	}
	if body.Scopes[0].Value != "/work/a" || body.Scopes[1].Value != "/work/b" {
		t.Fatalf("scope values = %q, %q; want /work/a, /work/b (sorted, deduped)", body.Scopes[0].Value, body.Scopes[1].Value)
	}
	if body.Scopes[0].Kind != string(sourcespkg.ScopeKindWorkspace) {
		t.Fatalf("scope kind = %q, want workspace", body.Scopes[0].Kind)
	}
}

func TestScopesEmptyWhenStoreEmpty(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	body := getScopes(ctx, t, server.URL+"/api/scopes")
	if len(body.Scopes) != 0 {
		t.Fatalf("scopes = %d, want 0 for an empty store", len(body.Scopes))
	}
}

func TestServesSearchPage(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := httptest.NewServer(web.NewServer(memoriespkg.NewRecaller(memories), "").Handler())
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) == 0 {
		t.Fatal("search page is empty")
	}
}

// getRecall fetches /api/recall at url and decodes the JSON contract. It fails
// the test on any transport, status, or decode error.
func getRecall(ctx context.Context, t *testing.T, url string) recallBody {
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
	var body recallBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode recall response: %v", err)
	}
	return body
}

// getScopes fetches /api/scopes at url and decodes the JSON contract. It fails
// the test on any transport, status, or decode error.
func getScopes(ctx context.Context, t *testing.T, url string) scopesBody {
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
	var body scopesBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode scopes response: %v", err)
	}
	return body
}

// --- store-backed test helpers (mirror internal/mcp and internal/memory setup) ---

func openStores(ctx context.Context, t *testing.T, path string) (*sourcespkg.Store, *memoriespkg.Store, func()) {
	t.Helper()
	tok, err := tokenize.NewKorean()
	if err != nil {
		t.Fatal(err)
	}
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

// closeResp closes an HTTP response body and fails the test if the close errors,
// so the linter's error-checking stays satisfied without silencing real failures.
func closeResp(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
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
