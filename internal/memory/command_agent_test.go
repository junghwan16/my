package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentItemsPlainTextIsSingleSummary(t *testing.T) {
	items := parseAgentItems("just a prose summary")
	if len(items) != 1 {
		t.Fatalf("items length = %d, want 1", len(items))
	}
	if items[0].Kind != ItemKindSummary {
		t.Fatalf("item kind = %q, want %q", items[0].Kind, ItemKindSummary)
	}
	if items[0].Text != "just a prose summary" {
		t.Fatalf("item text = %q", items[0].Text)
	}
}

func TestParseAgentItemsJSONArrayProducesMultipleItems(t *testing.T) {
	items := parseAgentItems(`[
		{"kind":"summary","text":"first","metadata_json":{"k":"v"}},
		{"text":"second"},
		{"text":"   "}
	]`)
	if len(items) != 2 {
		t.Fatalf("items length = %d, want 2 (blank item dropped)", len(items))
	}
	if items[0].Text != "first" || items[1].Text != "second" {
		t.Fatalf("unexpected texts: %q, %q", items[0].Text, items[1].Text)
	}
	if items[1].Kind != ItemKindSummary {
		t.Fatalf("defaulted kind = %q, want %q", items[1].Kind, ItemKindSummary)
	}
	if string(items[0].MetadataJSON) != `{"k":"v"}` {
		t.Fatalf("metadata = %s, want {\"k\":\"v\"}", items[0].MetadataJSON)
	}
}

func TestParseAgentItemsMalformedJSONFallsBackToSummary(t *testing.T) {
	text := `[not valid json`
	items := parseAgentItems(text)
	if len(items) != 1 || items[0].Text != text {
		t.Fatalf("items = %#v, want single summary of raw text", items)
	}
}

func TestTruncateUTF8DoesNotSplitRunes(t *testing.T) {
	// "가" is 3 bytes in UTF-8; capping mid-rune must back off to a boundary.
	s := strings.Repeat("가", 5) // 15 bytes
	got := truncateUTF8(s, 7)    // 7 bytes lands inside the third rune
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

func TestCommandAgentIngestStoresStdoutNotStderr(t *testing.T) {
	agent := newScriptAgent(t, "stdout-only", "printf 'real summary'; printf 'progress noise' 1>&2\n")

	out, err := agent.Ingest(context.Background(), AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items length = %d, want 1", len(out.Items))
	}
	if out.Items[0].Text != "real summary" {
		t.Fatalf("item text = %q, want 'real summary' (stderr must be excluded)", out.Items[0].Text)
	}
}

func TestCommandAgentIngestSurfacesStderrOnFailure(t *testing.T) {
	agent := newScriptAgent(t, "failer", "printf 'boom happened' 1>&2; exit 3\n")

	_, err := agent.Ingest(context.Background(), AgentInput{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom happened") {
		t.Fatalf("error = %v, want it to contain stderr 'boom happened'", err)
	}
}

func TestCommandAgentIngestParsesJSONArrayOutput(t *testing.T) {
	agent := newScriptAgent(t, "json", `printf '[{"text":"a"},{"text":"b"}]'`+"\n")

	out, err := agent.Ingest(context.Background(), AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items length = %d, want 2", len(out.Items))
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
