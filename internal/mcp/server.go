// Package mcp exposes the memory store to LLM agents over the Model Context
// Protocol. It runs an MCP server on stdio with a single recall tool that
// recalls recorded Memory within an optional Scope and returns ranked results,
// each carrying the Source context it derives from.
//
// The tool reuses the shared recall seam (memory.Recaller.Recollect), the same
// one the `my memory recall` CLI uses, so both surfaces return the same shape.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/junghwan16/my/internal/memory"
)

// serverName and serverVersion identify this MCP server to clients.
const (
	serverName    = "my-memory"
	serverVersion = "0.1.0"
)

// recaller finds relevant Memory within a Scope and attaches the Source context
// each Memory derives from. *memory.Recaller satisfies it, so the tool reuses
// the recall engine rather than re-implementing search or source resolution.
type recaller interface {
	Recollect(ctx context.Context, task, scope string, limit int) ([]memory.Recollection, error)
}

// Server serves the recall tool over MCP.
type Server struct {
	recaller recaller
}

// NewServer wires a recall server over the shared recall seam.
func NewServer(recaller recaller) *Server {
	return &Server{recaller: recaller}
}

// RecallInput is the recall tool's input. Only query is required; an empty scope
// searches every Scope and a non-positive limit falls back to the store default.
type RecallInput struct {
	Query string `json:"query" jsonschema:"natural-language task text to recall relevant memory for"`
	Scope string `json:"scope,omitempty" jsonschema:"optional Scope value to restrict recall to (empty searches every Scope)"`
	Limit int    `json:"limit,omitempty" jsonschema:"optional maximum number of memories to return"`
}

// RecalledScope mirrors source.Scope: the boundary a Source (and thus a recalled
// Memory) applies within.
type RecalledScope struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// RecalledSource is one Source a recalled Memory derives from. It mirrors
// memory.SourceRef so the MCP surface matches the CLI recall JSON.
type RecalledSource struct {
	ID    string        `json:"id"`
	URI   string        `json:"uri"`
	Scope RecalledScope `json:"scope"`
}

// RecalledMemory is one ranked recall result. Its fields mirror
// memory.Recollection: the Memory (ID, Agent, Kind, Text, Created) plus the
// Sources it was linked from. A Memory can carry more than one Source.
type RecalledMemory struct {
	MemoryID string           `json:"memory_id"`
	Agent    string           `json:"agent"`
	Kind     string           `json:"kind"`
	Text     string           `json:"text"`
	Created  string           `json:"created"`
	Sources  []RecalledSource `json:"sources"`
}

// RecallOutput is the recall tool's structured result: memories ranked
// best-first.
type RecallOutput struct {
	Memories []RecalledMemory `json:"memories"`
}

// errEmptyQuery is returned when recall is called without a query.
var errEmptyQuery = errors.New("recall requires a non-empty query")

// Recall runs the recall tool: it recalls memory within the optional scope and
// maps each Recollection to the tool output. It reuses the shared recall seam
// and does not resolve sources itself. It is exported so tests can exercise the
// handler end-to-end without a stdio round-trip.
func (s *Server) Recall(ctx context.Context, in RecallInput) (RecallOutput, error) {
	if in.Query == "" {
		return RecallOutput{}, errEmptyQuery
	}

	recollections, err := s.recaller.Recollect(ctx, in.Query, in.Scope, in.Limit)
	if err != nil {
		return RecallOutput{}, fmt.Errorf("recall: %w", err)
	}

	memories := make([]RecalledMemory, 0, len(recollections))
	for i := range recollections {
		memories = append(memories, describe(recollections[i]))
	}
	return RecallOutput{Memories: memories}, nil
}

// describe maps a Recollection to a tool result, carrying every attached Source.
func describe(r memory.Recollection) RecalledMemory {
	sources := make([]RecalledSource, 0, len(r.Sources))
	for _, ref := range r.Sources {
		sources = append(sources, RecalledSource{
			ID:  string(ref.ID),
			URI: ref.URI,
			Scope: RecalledScope{
				Kind:  string(ref.Scope.Kind),
				Value: ref.Scope.Value,
			},
		})
	}
	return RecalledMemory{
		MemoryID: string(r.MemoryID),
		Agent:    r.Agent,
		Kind:     string(r.Kind),
		Text:     r.Text,
		Created:  r.CreatedAt.UTC().Format(time.RFC3339),
		Sources:  sources,
	}
}

// Run registers the recall tool and serves it over the given transport until the
// client disconnects or the context is cancelled. Callers pass an
// *mcpsdk.StdioTransport to serve over stdio.
func (s *Server) Run(ctx context.Context, transport mcpsdk.Transport) error {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: serverName, Version: serverVersion}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "recall",
		Description: "Recall relevant Memory for a natural-language query within an optional Scope, ranked best-first. Each result carries its Memory identifier, agent, kind, text, creation time, and the Sources it derives from.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in RecallInput) (*mcpsdk.CallToolResult, RecallOutput, error) {
		out, err := s.Recall(ctx, in)
		if err != nil {
			return nil, RecallOutput{}, err
		}
		return nil, out, nil
	})
	if err := server.Run(ctx, transport); err != nil {
		return fmt.Errorf("run mcp server: %w", err)
	}
	return nil
}
