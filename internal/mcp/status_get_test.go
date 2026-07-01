package mcp_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/junghwan16/gieok/internal/mcp"
	"github.com/junghwan16/gieok/internal/memory"
)

func TestStatusReportsIndexHealth(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")
	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:b", "/work/b"), "memory:b", "날씨 정보")

	server := mcp.NewServer(memory.NewRecaller(memories))

	out, err := server.Status(ctx, mcp.StatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memories != 2 {
		t.Fatalf("memories = %d, want 2", out.Memories)
	}
	if out.FTSRows != 2 {
		t.Fatalf("fts_rows = %d, want 2", out.FTSRows)
	}
	if out.Vectors != 0 {
		t.Fatalf("vectors = %d, want 0 (no embedder attached)", out.Vectors)
	}
}

func TestGetReturnsMemoryByID(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "코스피 종목 분석 리포트")

	server := mcp.NewServer(memory.NewRecaller(memories))

	out, err := server.Get(ctx, mcp.GetInput{MemoryID: "memory:a"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.Memory == nil {
		t.Fatalf("get = %#v, want found with a memory", out)
	}
	if out.Memory.MemoryID != "memory:a" {
		t.Fatalf("memory id = %q, want memory:a", out.Memory.MemoryID)
	}
	if out.Memory.Text != "코스피 종목 분석 리포트" {
		t.Fatalf("text = %q, want the recorded text", out.Memory.Text)
	}
	if out.Memory.Agent != "t" || out.Memory.Kind != string(memory.MemoryKindSummary) {
		t.Fatalf("agent/kind = %q/%q, want t/summary", out.Memory.Agent, out.Memory.Kind)
	}
	if out.Memory.Created == "" {
		t.Fatal("created is empty, want an RFC3339 timestamp")
	}
	if len(out.Memory.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(out.Memory.Sources))
	}
	if out.Memory.Sources[0].ID != "codex_session:a" {
		t.Fatalf("source id = %q, want codex_session:a", out.Memory.Sources[0].ID)
	}
	if out.Memory.Sources[0].Scope.Value != "/work/a" {
		t.Fatalf("source scope value = %q, want /work/a", out.Memory.Sources[0].Scope.Value)
	}
}

func TestGetReportsNotFound(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "종목 분석")

	server := mcp.NewServer(memory.NewRecaller(memories))

	out, err := server.Get(ctx, mcp.GetInput{MemoryID: "memory:missing"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found || out.Memory != nil {
		t.Fatalf("get = %#v, want not-found with no memory", out)
	}
	if out.Message == "" {
		t.Fatal("not-found result has empty message, want an explanation")
	}
}

func TestGetEmptyIDErrors(t *testing.T) {
	ctx := context.Background()
	_, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	server := mcp.NewServer(memory.NewRecaller(memories))

	if _, err := server.Get(ctx, mcp.GetInput{MemoryID: ""}); err == nil {
		t.Fatal("empty memory_id returned no error, want an error")
	}
}
