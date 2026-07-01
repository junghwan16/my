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
)

// assets holds the static search page served at /. It is embedded so the binary
// is fully self-contained and works offline with no external assets.
//
//go:embed static
var assets embed.FS

// recaller finds relevant Memory within a Scope for a task, attaching the Source
// context each Memory derives from. *memories.Recaller satisfies it, so the HTTP
// handler reuses the shared recall path rather than re-implementing ranking.
type recaller interface {
	Recall(ctx context.Context, task, scope string, limit int) ([]memoriespkg.RecallResult, error)
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

// routes builds the request multiplexer: the static search page at / and the
// recall JSON API at /api/recall.
func (s *Server) routes() http.Handler {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		// The static directory is embedded at build time, so this can only fail
		// on a broken build; panicking surfaces that immediately.
		panic(fmt.Sprintf("web: embed static assets: %v", err))
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/recall", s.handleRecall)
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
