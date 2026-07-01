package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentMemoriesPlainTextIsSingleSummary(t *testing.T) {
	memories := parseAgentMemories("just a prose summary")
	if len(memories) != 1 {
		t.Fatalf("memories length = %d, want 1", len(memories))
	}
	if memories[0].Kind != MemoryKindSummary {
		t.Fatalf("memory kind = %q, want %q", memories[0].Kind, MemoryKindSummary)
	}
	if memories[0].Text != "just a prose summary" {
		t.Fatalf("memory text = %q", memories[0].Text)
	}
}

func TestParseAgentMemoriesJSONArrayProducesMultiple(t *testing.T) {
	memories := parseAgentMemories(`[
		{"kind":"summary","text":"first","metadata_json":{"k":"v"}},
		{"text":"second"},
		{"text":"   "}
	]`)
	if len(memories) != 2 {
		t.Fatalf("memories length = %d, want 2 (blank memory dropped)", len(memories))
	}
	if memories[0].Text != "first" || memories[1].Text != "second" {
		t.Fatalf("unexpected texts: %q, %q", memories[0].Text, memories[1].Text)
	}
	if memories[1].Kind != MemoryKindSummary {
		t.Fatalf("defaulted kind = %q, want %q", memories[1].Kind, MemoryKindSummary)
	}
	if string(memories[0].MetadataJSON) != `{"k":"v"}` {
		t.Fatalf("metadata = %s, want {\"k\":\"v\"}", memories[0].MetadataJSON)
	}
}

func TestParseAgentMemoriesMalformedJSONFallsBackToSummary(t *testing.T) {
	text := `[not valid json`
	memories := parseAgentMemories(text)
	if len(memories) != 1 || memories[0].Text != text {
		t.Fatalf("memories = %#v, want single summary of raw text", memories)
	}
}

func TestTruncateUTF8DoesNotSplitRunes(t *testing.T) {
	// "가" is 3 bytes in UTF-8; capping mid-rune must back off to a boundary.
	s := strings.Repeat("가", 5) // 15 bytes
	got := truncateUTF8(s, 7)   // 7 bytes lands inside the third rune
	if !isValidBoundary(got) {
		t.Fatalf("truncated string is not valid UTF-8: %q", got)
	}
	if len([]rune(got)) != 2 {
		t.Fatalf("rune count = %d, want 2", len([]rune(got)))
	}
}

func isValidBoundary(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// fakeRunner is an in-memory transport so Ingest's orchestration (prompt build,
// runner wiring, output parsing, empty/error handling) can be exercised without
// spawning a process.
type fakeRunner struct {
	stdout []byte
	err    error
	prompt string // captured for assertions
}

func (r *fakeRunner) Run(_ context.Context, prompt string) ([]byte, error) {
	r.prompt = prompt
	return r.stdout, r.err
}

func TestCommandAgentIngestParsesJSONArrayOutput(t *testing.T) {
	agent := CommandAgent{name: "json", runner: &fakeRunner{stdout: []byte(`[{"text":"a"},{"text":"b"}]`)}}

	out, err := agent.Ingest(context.Background(), AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 2 {
		t.Fatalf("memories length = %d, want 2", len(out.Memories))
	}
}

func TestCommandAgentIngestEmptyOutputProducesNoMemories(t *testing.T) {
	agent := CommandAgent{name: "empty", runner: &fakeRunner{stdout: []byte("   \n")}}

	out, err := agent.Ingest(context.Background(), AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 0 {
		t.Fatalf("memories length = %d, want 0 for blank output", len(out.Memories))
	}
}

func TestCommandAgentIngestPropagatesRunnerError(t *testing.T) {
	agent := CommandAgent{name: "failer", runner: &fakeRunner{err: errors.New("transport boom")}}

	_, err := agent.Ingest(context.Background(), AgentInput{})
	if err == nil || !strings.Contains(err.Error(), "transport boom") {
		t.Fatalf("error = %v, want it to propagate 'transport boom'", err)
	}
}

func TestExecRunnerStoresStdoutNotStderr(t *testing.T) {
	agent := newScriptAgent(t, "stdout-only", "printf 'real summary'; printf 'progress noise' 1>&2\n")

	out, err := agent.Ingest(context.Background(), AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Memories) != 1 {
		t.Fatalf("memories length = %d, want 1", len(out.Memories))
	}
	if out.Memories[0].Text != "real summary" {
		t.Fatalf("memory text = %q, want 'real summary' (stderr must be excluded)", out.Memories[0].Text)
	}
}

func TestExecRunnerSurfacesStderrOnFailure(t *testing.T) {
	agent := newScriptAgent(t, "failer", "printf 'boom happened' 1>&2; exit 3\n")

	_, err := agent.Ingest(context.Background(), AgentInput{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom happened") {
		t.Fatalf("error = %v, want it to contain stderr 'boom happened'", err)
	}
}

func newScriptAgent(t *testing.T, name, body string) CommandAgent {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewCommandAgent(name, path)
}
