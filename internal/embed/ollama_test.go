package embed_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/junghwan16/gieok/internal/embed"
)

// TestEmbedParsesOllamaResponse drives the adapter against a fake HTTP server
// (no real Ollama, no network beyond loopback) to prove it posts to /api/embed
// with the configured model and parses embeddings[0].
func TestEmbedParsesOllamaResponse(t *testing.T) {
	ctx := context.Background()
	var gotPath, gotModel, gotInput string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel, gotInput = req.Model, req.Input
		if err := json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	embedder := embed.NewOllama(embed.WithBaseURL(server.URL), embed.WithModel("bge-m3"))
	vec, err := embedder.Embed(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/embed" {
		t.Fatalf("request path = %q, want /api/embed", gotPath)
	}
	if gotModel != "bge-m3" || gotInput != "hello" {
		t.Fatalf("request model=%q input=%q, want bge-m3/hello", gotModel, gotInput)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Fatalf("embedding = %v, want [0.1 0.2 0.3]", vec)
	}
	if embedder.Model() != "bge-m3" {
		t.Fatalf("model = %q, want bge-m3", embedder.Model())
	}
}

// TestEmbedErrorsOnNon200 proves a non-200 status surfaces as an error so the
// caller can fall back to lexical recall.
func TestEmbedErrorsOnNon200(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	embedder := embed.NewOllama(embed.WithBaseURL(server.URL))
	if _, err := embedder.Embed(ctx, "hello"); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

// TestAvailableReportsUnreachable proves Available is false when no server
// answers, so an absent Ollama sidecar disables semantic recall gracefully.
func TestAvailableReportsUnreachable(t *testing.T) {
	ctx := context.Background()
	// Reserved TEST-NET-1 address with a closed port: connection fails fast.
	// A short client timeout keeps the test quick if the host drops the packet.
	embedder := embed.NewOllama(
		embed.WithBaseURL("http://192.0.2.1:1"),
		embed.WithHTTPClient(&http.Client{Timeout: 500 * time.Millisecond}),
	)
	if embedder.Available(ctx) {
		t.Fatal("Available = true for unreachable server, want false")
	}
}
