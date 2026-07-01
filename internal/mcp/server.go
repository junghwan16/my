// Package mcp exposes the memory store to LLM agents over the Model Context
// Protocol. It runs an MCP server on stdio with three tools: recall (rank Memory
// for a natural-language query within an optional Scope), status (report recall
// index health), and get (fetch one Memory by id). Each recalled or fetched
// Memory carries the Source context it derives from.
//
// The tools reuse the shared recall seams on memory.Recaller (Recollect, Stats,
// Get), the same ones the `gieok memory recall` CLI uses, so every surface
// returns the same shape.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/junghwan16/gieok/internal/memory"
)

// serverName and serverVersion identify this MCP server to clients.
const (
	serverName    = "gieok-memory"
	serverVersion = "0.1.0"
)

// recaller finds relevant Memory within a Scope and attaches the Source context
// each Memory derives from, reports recall index health, and fetches one Memory
// by id. *memory.Recaller satisfies it, so the tools reuse the recall engine
// rather than re-implementing search or source resolution.
type recaller interface {
	Recollect(ctx context.Context, task, scope string, limit int) ([]memory.Recollection, error)
	Stats(ctx context.Context) (memory.Stats, error)
	Get(ctx context.Context, id memory.MemoryID) (memory.Recollection, bool, error)
}

// compile-time check that *memory.Recaller satisfies the consumed interface.
var _ recaller = (*memory.Recaller)(nil)

// Server serves the recall, status, and get tools over MCP.
type Server struct {
	recaller recaller
}

// NewServer wires a recall server over the shared recall seams.
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

// StatusInput is the status tool's input. The tool takes no parameters; the
// empty struct gives the SDK a schema to bind to.
type StatusInput struct{}

// StatusOutput is the status tool's structured result: the recall index health
// counts. A healthy store has Vectors and FTSRows close to Memories.
type StatusOutput struct {
	Memories int `json:"memories"`
	Vectors  int `json:"vectors"`
	FTSRows  int `json:"fts_rows"`
}

// Status runs the status tool: it reports recall index health (memory, vector,
// and full-text index row counts). It reuses the shared Stats seam and is
// exported so tests can exercise the handler without a stdio round-trip.
func (s *Server) Status(ctx context.Context, _ StatusInput) (StatusOutput, error) {
	stats, err := s.recaller.Stats(ctx)
	if err != nil {
		return StatusOutput{}, fmt.Errorf("status: %w", err)
	}
	return StatusOutput{
		Memories: stats.Memories,
		Vectors:  stats.Vectors,
		FTSRows:  stats.FTSRows,
	}, nil
}

// GetInput is the get tool's input: the Memory identifier to fetch.
type GetInput struct {
	MemoryID string `json:"memory_id" jsonschema:"the Memory identifier to fetch"`
}

// GetOutput is the get tool's structured result. Found reports whether a Memory
// with the requested id exists; when false, Memory is absent and Message
// explains the miss so a client renders a clean "not found".
type GetOutput struct {
	Found   bool            `json:"found"`
	Message string          `json:"message,omitempty"`
	Memory  *RecalledMemory `json:"memory,omitempty"`
}

// errEmptyMemoryID is returned when get is called without a memory_id.
var errEmptyMemoryID = errors.New("get requires a non-empty memory_id")

// Get runs the get tool: it fetches one Memory by id in the same per-memory
// shape recall uses, carrying every Source it derives from. A missing id yields
// a found=false result with a message rather than an error. It reuses the shared
// Get seam and is exported so tests can exercise the handler directly.
func (s *Server) Get(ctx context.Context, in GetInput) (GetOutput, error) {
	if in.MemoryID == "" {
		return GetOutput{}, errEmptyMemoryID
	}

	recollection, found, err := s.recaller.Get(ctx, memory.MemoryID(in.MemoryID))
	if err != nil {
		return GetOutput{}, fmt.Errorf("get: %w", err)
	}
	if !found {
		return GetOutput{Found: false, Message: "no memory found for id " + in.MemoryID}, nil
	}
	got := describe(recollection)
	return GetOutput{Found: true, Memory: &got}, nil
}

// Run registers the recall, status, and get tools and serves them over the given
// transport until the client disconnects or the context is cancelled. Callers
// pass an *mcpsdk.StdioTransport to serve over stdio.
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
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "status",
		Description: "Report recall index health: the number of stored memories, embedding vectors, and full-text index rows. A large gap between memories and vectors or fts_rows flags an index that needs a backfill.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in StatusInput) (*mcpsdk.CallToolResult, StatusOutput, error) {
		out, err := s.Status(ctx, in)
		if err != nil {
			return nil, StatusOutput{}, err
		}
		return nil, out, nil
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get",
		Description: "Fetch one Memory by its identifier in the same shape recall returns: memory id, agent, kind, text, creation time, and the Sources it derives from. Returns found=false with a message when no Memory has the id.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in GetInput) (*mcpsdk.CallToolResult, GetOutput, error) {
		out, err := s.Get(ctx, in)
		if err != nil {
			return nil, GetOutput{}, err
		}
		return nil, out, nil
	})
	if err := server.Run(ctx, transport); err != nil {
		return fmt.Errorf("run mcp server: %w", err)
	}
	return nil
}
