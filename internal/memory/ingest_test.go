package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/junghwan16/my/internal/memory"
)

func TestIngestSourcesRunsAgentsInParallelAndLinksItems(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	source := memory.Source{
		ID:            "codex_session:test",
		Kind:          memory.SourceKindCodexSession,
		URI:           "memory://test/source",
		ContentSHA256: "hash",
		Scope: memory.Scope{
			Kind:  memory.ScopeKindWorkspace,
			Value: "/work/project",
		},
		RecordedAt:   time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		MetadataJSON: json.RawMessage(`{}`),
	}
	events := []memory.SourceEvent{{
		SourceID:    source.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        "build ingest",
		PayloadJSON: json.RawMessage(`{"text":"build ingest"}`),
		RawJSON:     json.RawMessage(`{"type":"response_item"}`),
	}}
	if err := store.RecordSource(ctx, source, events); err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 2)
	release := make(chan struct{})
	agents := []memory.Agent{
		blockingAgent{name: "claude", started: started, release: release},
		blockingAgent{name: "codex", started: started, release: release},
	}

	resultCh := make(chan ingestResult, 1)
	go func() {
		result, err := memory.IngestSources(ctx, store, agents, time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC), nil)
		resultCh <- ingestResult{result: result, err: err}
	}()

	seen := map[string]bool{}
	for range agents {
		select {
		case name := <-started:
			seen[name] = true
		case <-ctx.Done():
			t.Fatalf("agents did not start in parallel: %v", ctx.Err())
		}
	}
	close(release)

	run := <-resultCh
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Sources != 1 {
		t.Fatalf("ingested sources = %d, want 1", run.result.Sources)
	}
	if run.result.Items != 2 {
		t.Fatalf("ingested items = %d, want 2", run.result.Items)
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("started agents = %#v, want claude and codex", seen)
	}

	items, err := store.SourceItems(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("source items length = %d, want 2", len(items))
	}

	links, err := store.SourceLinks(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("source links length = %d, want 2", len(links))
	}
}

func TestIngestSourcesWithOptionsFiltersSources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	first := testSource("codex_session:first", "first")
	second := testSource("codex_session:second", "second")
	if err := store.RecordSource(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSource(ctx, second, nil); err != nil {
		t.Fatal(err)
	}

	result, err := memory.IngestSourcesWithOptions(
		ctx,
		store,
		[]memory.Agent{staticAgent{name: "fake"}},
		memory.IngestOptions{
			SourceIDs: []memory.SourceID{second.ID},
			Limit:     1,
		},
		time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sources != 1 {
		t.Fatalf("ingested sources = %d, want 1", result.Sources)
	}

	firstItems, err := store.SourceItems(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstItems) != 0 {
		t.Fatalf("first source items length = %d, want 0", len(firstItems))
	}

	secondItems, err := store.SourceItems(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondItems) != 1 {
		t.Fatalf("second source items length = %d, want 1", len(secondItems))
	}
}

func TestIngestSourcesStoresSuccessfulOutputWhenAnAgentFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	source := testSource("codex_session:test", "test")
	if err := store.RecordSource(ctx, source, nil); err != nil {
		t.Fatal(err)
	}

	result, err := memory.IngestSources(
		ctx,
		store,
		[]memory.Agent{
			staticAgent{name: "ok"},
			failingAgent{name: "pi"},
		},
		time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items != 1 {
		t.Fatalf("ingested items = %d, want 1", result.Items)
	}
	if result.Errors != 1 {
		t.Fatalf("agent errors = %d, want 1", result.Errors)
	}

	items, err := store.SourceItems(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("source items length = %d, want 1", len(items))
	}
}

func TestIngestReplacesStaleAgentItemsOnReingest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	source := testSource("codex_session:test", "test")
	if err := store.RecordSource(ctx, source, nil); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	if _, err := memory.IngestSources(ctx, store, []memory.Agent{staticAgent{name: "x", text: "v1"}}, now, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.IngestSources(ctx, store, []memory.Agent{staticAgent{name: "x", text: "v2"}}, now, nil); err != nil {
		t.Fatal(err)
	}

	items, err := store.SourceItems(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("source items length = %d, want 1 (stale item not replaced)", len(items))
	}
	if items[0].Text != "v2" {
		t.Fatalf("item text = %q, want v2", items[0].Text)
	}
}

func TestIngestSkipExistingSkipsAlreadyIngestedAgents(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	source := testSource("codex_session:test", "test")
	if err := store.RecordSource(ctx, source, nil); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	if _, err := memory.IngestSources(ctx, store, []memory.Agent{staticAgent{name: "x", text: "v1"}}, now, nil); err != nil {
		t.Fatal(err)
	}

	// The agent named "x" would fail if it ran, so a clean result proves it was skipped.
	result, err := memory.IngestSourcesWithOptions(
		ctx,
		store,
		[]memory.Agent{failingAgent{name: "x"}},
		memory.IngestOptions{SkipExisting: true},
		now,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items != 0 {
		t.Fatalf("ingested items = %d, want 0", result.Items)
	}
	if result.Errors != 0 {
		t.Fatalf("agent errors = %d, want 0 (agent should be skipped)", result.Errors)
	}

	items, err := store.SourceItems(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Text != "v1" {
		t.Fatalf("source items = %#v, want single v1 item", items)
	}
}

type ingestResult struct {
	result memory.IngestResult
	err    error
}

func testSource(id memory.SourceID, text string) memory.Source {
	return memory.Source{
		ID:            id,
		Kind:          memory.SourceKindCodexSession,
		URI:           "memory://test/" + text,
		ContentSHA256: "hash-" + text,
		Scope: memory.Scope{
			Kind:  memory.ScopeKindWorkspace,
			Value: "/work/project",
		},
		RecordedAt:   time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		MetadataJSON: json.RawMessage(`{}`),
	}
}

type staticAgent struct {
	name string
	text string
}

func (a staticAgent) Name() string {
	return a.name
}

func (a staticAgent) Ingest(context.Context, memory.AgentInput) (memory.AgentOutput, error) {
	text := a.text
	if text == "" {
		text = "static summary"
	}
	return memory.AgentOutput{
		Items: []memory.AgentItem{{
			Kind: memory.ItemKindSummary,
			Text: text,
		}},
	}, nil
}

type failingAgent struct {
	name string
}

func (a failingAgent) Name() string {
	return a.name
}

func (a failingAgent) Ingest(context.Context, memory.AgentInput) (memory.AgentOutput, error) {
	return memory.AgentOutput{}, assertAnError{}
}

type assertAnError struct{}

func (assertAnError) Error() string {
	return "agent failed"
}

type blockingAgent struct {
	name    string
	started chan<- string
	release <-chan struct{}
}

func (a blockingAgent) Name() string {
	return a.name
}

func (a blockingAgent) Ingest(ctx context.Context, input memory.AgentInput) (memory.AgentOutput, error) {
	select {
	case a.started <- a.name:
	case <-ctx.Done():
		return memory.AgentOutput{}, ctx.Err()
	}

	select {
	case <-a.release:
	case <-ctx.Done():
		return memory.AgentOutput{}, ctx.Err()
	}

	return memory.AgentOutput{
		Items: []memory.AgentItem{{
			Kind: memory.ItemKindSummary,
			Text: a.name + " summary for " + string(input.Source.ID),
		}},
	}, nil
}
