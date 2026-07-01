// Package embed provides the default, pure-Go embedder used for optional
// semantic recall. It calls a local Ollama HTTP server (no cgo, no SDK — just
// net/http), so the semantic layer is an optional sidecar: when Ollama is
// unreachable or the model is missing, construction/health-checks fail and the
// caller keeps lexical recall. It satisfies memory.Embedder.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/junghwan16/gieok/internal/memory"
)

// Ollama satisfies the memory.Embedder seam.
var _ memory.Embedder = (*Ollama)(nil)

// Defaults target a local Ollama running bge-m3 (dense, 1024-dim). bge-m3 is a
// strong multilingual (incl. Korean) embedding model that fits comfortably on
// an M1-class machine via Metal.
const (
	DefaultBaseURL = "http://localhost:11434"
	DefaultModel   = "bge-m3"
)

// errNoEmbedding reports that Ollama returned a response without an embedding.
var errNoEmbedding = errors.New("ollama returned no embedding")

// Ollama is an Embedder backed by a local Ollama HTTP server. It is safe for
// concurrent use: it holds only immutable configuration and an *http.Client.
type Ollama struct {
	baseURL string
	model   string
	client  *http.Client
}

// Option configures an Ollama embedder.
type Option func(*Ollama)

// WithBaseURL overrides the Ollama server base URL (default DefaultBaseURL).
func WithBaseURL(baseURL string) Option {
	return func(o *Ollama) {
		if baseURL != "" {
			o.baseURL = baseURL
		}
	}
}

// WithModel overrides the embedding model (default DefaultModel).
func WithModel(model string) Option {
	return func(o *Ollama) {
		if model != "" {
			o.model = model
		}
	}
}

// WithHTTPClient overrides the HTTP client, e.g. to set a custom timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(o *Ollama) {
		if client != nil {
			o.client = client
		}
	}
}

// NewOllama builds an Ollama embedder with sensible local defaults. It does not
// contact the server; use Available to health-check before relying on it so an
// unreachable server degrades to lexical-only recall.
func NewOllama(opts ...Option) *Ollama {
	o := &Ollama{
		baseURL: DefaultBaseURL,
		model:   DefaultModel,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Model returns the configured embedding model id.
func (o *Ollama) Model() string {
	return o.model
}

// Available reports whether the embedder can produce vectors: it performs one
// real embed to confirm both that Ollama is reachable and that the model is
// installed. Callers use it to decide whether to attach the embedder, so a
// missing sidecar disables semantic recall instead of failing every write.
func (o *Ollama) Available(ctx context.Context) bool {
	vec, err := o.Embed(ctx, "health check")
	return err == nil && len(vec) > 0
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns the dense embedding of text from Ollama's /api/embed endpoint.
func (o *Ollama) Embed(ctx context.Context, text string) (vector []float32, err error) {
	body, err := json.Marshal(embedRequest{Model: o.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ollama embed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close ollama response body: %w", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		snippet, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return nil, fmt.Errorf("read ollama embed error body (status %d): %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("ollama embed status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}

	var decoded embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ollama embed response: %w", err)
	}
	if len(decoded.Embeddings) == 0 || len(decoded.Embeddings[0]) == 0 {
		return nil, errNoEmbedding
	}
	return decoded.Embeddings[0], nil
}
