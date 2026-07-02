// Package web serves gieok memory over a local, offline HTTP surface. It runs a
// Go HTTP server bound to loopback that serves a static search page (embedded
// with go:embed, no CDN or external service) and a JSON API. The /api/recall
// endpoint passes straight through the shared memories.Recaller.Recall seam
// (hybrid RRF, ADR-0006) — the same one the CLI and MCP tools use — so the web
// surface returns the same ranking without re-implementing any search logic.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// assets holds the static search page served at /. It is embedded so the binary
// is fully self-contained and works offline with no external assets.
//
//go:embed static
var assets embed.FS

// recaller finds relevant Memory within a Scope for a task, attaching the Source
// context each Memory derives from, and lists the Scopes the store holds so the
// search page can offer a scope selector. *memories.Recaller satisfies it, so the
// HTTP handlers reuse the shared recall path and read-model rather than
// re-implementing ranking or querying the database directly.
type recaller interface {
	Recall(ctx context.Context, task, scope string, limit int) ([]memoriespkg.RecallResult, error)
	Scopes(ctx context.Context) ([]sourcespkg.Scope, error)
	Graph(ctx context.Context, scope string, cap int) (memoriespkg.Graph, error)
	MemoryNeighborhood(ctx context.Context, id memoriespkg.MemoryID) (memoriespkg.Graph, bool, error)
	EditMemory(ctx context.Context, id memoriespkg.MemoryID, override string) (memoriespkg.RecallResult, bool, error)
	Get(ctx context.Context, id memoriespkg.MemoryID) (memoriespkg.RecallResult, bool, error)
}

// compile-time check that *memories.Recaller satisfies the consumed interface.
var _ recaller = (*memoriespkg.Recaller)(nil)

// Server serves the search page and the /api/recall JSON API over HTTP.
type Server struct {
	recaller     recaller
	defaultScope string
	handler      http.Handler
}

// NewServer wires an HTTP server over the shared recall seam. defaultScope is the
// Scope /api/recall restricts to when a request omits the scope parameter; pass
// the server's workspace directory to mirror the CLI default, or an empty string
// to span every Scope. A request may still override it with an explicit scope
// parameter (empty spans every Scope).
func NewServer(r recaller, defaultScope string) *Server {
	s := &Server{recaller: r, defaultScope: defaultScope}
	s.handler = s.routes()
	return s
}

// Handler returns the HTTP handler serving the search page and JSON API. It is
// exported so tests can drive the server with httptest without binding a port.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// routes builds the request multiplexer: the static search page at /, the recall
// JSON API at /api/recall, and the scope list at /api/scopes that backs the
// search page's scope selector.
func (s *Server) routes() http.Handler {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		// The static directory is embedded at build time, so this can only fail
		// on a broken build; panicking surfaces that immediately.
		panic(fmt.Sprintf("web: embed static assets: %v", err))
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	// Serve the graph page at the clean /graph path (the file is graph.html), so
	// the provenance view has a shareable URL alongside the search page at /.
	mux.HandleFunc("/graph", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/graph.html"
		http.FileServer(http.FS(static)).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/recall", s.handleRecall)
	mux.HandleFunc("/api/scopes", s.handleScopes)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/graph/memory", s.handleMemoryNeighborhood)
	mux.HandleFunc("/api/memory", s.handleMemoryGet)
	mux.HandleFunc("/api/memory/edit", s.handleMemoryEdit)
	return mux
}

// recallResponse is the /api/recall JSON body. It wraps the ranked memories
// under a "memories" key, matching the CLI recall JSON so every surface (CLI,
// MCP, web) returns the same structure.
type recallResponse struct {
	Memories []memoriespkg.RecallResult `json:"memories"`
}

// handleRecall runs the recall tool over HTTP: it reads the query, scope, and
// limit from the request, passes them straight through the shared recall seam,
// and writes the ranked memories as JSON. An empty query is not an error — it
// takes the shared recent-Memory path — so the search page shows recent Memory
// before the user types anything.
func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	scope := s.scope(r)

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results, err := s.recaller.Recall(r.Context(), query, scope, limit)
	if err != nil {
		http.Error(w, "recall failed", http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []memoriespkg.RecallResult{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(recallResponse{Memories: results}); err != nil {
		// The response is already partially written, so we can only log-shaped
		// signal by writing nothing further; the client sees a truncated body.
		return
	}
}

// scopesResponse is the /api/scopes JSON body. It wraps the distinct Scopes the
// store holds under a "scopes" key, each with its kind and value, so the search
// page can populate a scope selector. The all-scopes option is the empty scope,
// which the UI adds itself rather than the store returning it.
type scopesResponse struct {
	Scopes []scopeDTO `json:"scopes"`
}

// scopeDTO is one Scope in the /api/scopes response. Its value is exactly the
// string /api/recall filters on when passed as the scope parameter.
type scopeDTO struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// handleScopes serves the distinct Scopes the store holds, so the search page can
// offer a scope selector. It reads the shared read-model (never the recall path)
// and writes them as JSON. The all-scopes choice is not in the list — it is the
// empty scope, which the UI offers itself and passes back as an empty scope
// parameter.
func (s *Server) handleScopes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scopes, err := s.recaller.Scopes(r.Context())
	if err != nil {
		http.Error(w, "load scopes failed", http.StatusInternalServerError)
		return
	}

	dtos := make([]scopeDTO, 0, len(scopes))
	for _, scope := range scopes {
		dtos = append(dtos, scopeDTO{Kind: string(scope.Kind), Value: scope.Value})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(scopesResponse{Scopes: dtos}); err != nil {
		// The response is already partially written, so we can only stop; the
		// client sees a truncated body.
		return
	}
}

// handleGraph serves the scope-scoped provenance graph for the /graph page:
// Source and Memory nodes with Link (Source->Memory) and Relation
// (Memory<->Memory) edges, Source nodes sized by fan-out and Memory nodes by
// Relation degree, plus an aggregate panel (total Sources, Memories, average
// Sources per Memory) computed over the whole scope regardless of the node cap.
// It reads the graph read-model (never the recall path) and writes it as JSON.
// The scope parameter follows /api/recall (absent uses the default, present-but-
// empty spans every scope); the cap parameter bounds the returned node count and
// falls back to the store default when absent.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scope := s.scope(r)
	cap, err := parseLimit(r.URL.Query().Get("cap"))
	if err != nil {
		http.Error(w, "cap must be a non-negative integer", http.StatusBadRequest)
		return
	}

	graph, err := s.recaller.Graph(r.Context(), scope, cap)
	if err != nil {
		http.Error(w, "graph failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, graph)
}

// handleMemoryNeighborhood serves the click-to-expand drilldown for one Memory:
// the Memory node, every Source that melted into it with their Link edges, and
// every Memory it connects to by a Relation with those Relation edges, all
// unrestricted by scope so expanding reveals the Memory's full provenance and
// Relations. It
// reads the id parameter, loads the neighborhood from the read-model, and writes
// it as JSON. A missing id is a client error; an unknown Memory is 404 so the UI
// can render a clean miss.
func (s *Server) handleMemoryNeighborhood(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	graph, found, err := s.recaller.MemoryNeighborhood(r.Context(), memoriespkg.MemoryID(id))
	if err != nil {
		http.Error(w, "graph failed", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "memory not found", http.StatusNotFound)
		return
	}

	writeJSON(w, graph)
}

// handleMemoryGet returns one Memory by id as a RecallResult, so the graph can
// deep-link a node to the recall view. A missing id is a client error; an
// unknown Memory is 404.
func (s *Server) handleMemoryGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	result, found, err := s.recaller.Get(r.Context(), memoriespkg.MemoryID(id))
	if err != nil {
		http.Error(w, "get failed", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "memory not found", http.StatusNotFound)
		return
	}

	writeJSON(w, result)
}

// editRequest is the /api/memory/edit body: the Memory to edit and the human
// Override text. An empty text clears the Override, restoring the agent's
// original memory.
type editRequest struct {
	MemoryID string `json:"memory_id"`
	Text     string `json:"text"`
}

// handleMemoryEdit layers or clears a human Override on a Memory (ADR-0010),
// leaving its hashed identity and provenance untouched, and writes the updated
// result. It is the one write endpoint on the web surface. A missing Memory is
// 404 so the UI can render a clean miss.
func (s *Server) handleMemoryEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req editRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(req.MemoryID)
	if id == "" {
		http.Error(w, "memory_id is required", http.StatusBadRequest)
		return
	}

	result, found, err := s.recaller.EditMemory(r.Context(), memoriespkg.MemoryID(id), req.Text)
	if err != nil {
		http.Error(w, "edit failed", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "memory not found", http.StatusNotFound)
		return
	}

	writeJSON(w, result)
}

// writeJSON encodes v as the JSON response body with the shared content type. A
// mid-write encode failure leaves the client a truncated body — the response is
// already partially written, so there is nothing further to signal.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}

// scope resolves the Scope a recall request restricts to: an explicit scope
// parameter (present but empty spans every Scope), or the server's default Scope
// when the parameter is absent.
func (s *Server) scope(r *http.Request) string {
	if !r.URL.Query().Has("scope") {
		return s.defaultScope
	}
	return strings.TrimSpace(r.URL.Query().Get("scope"))
}

// parseLimit reads the optional limit parameter. An empty value means "use the
// store default" (0); a non-numeric or negative value is a client error.
func parseLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0, fmt.Errorf("limit must be a non-negative integer")
	}
	return limit, nil
}
