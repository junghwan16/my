package memories_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
)

// TestIngestReflectsLateSessionContent is the #7 regression: a keyword that
// appears only late in a session (well past the old 40-event leading window)
// must reach Memory and be recallable. The prompt-echoing agent stores the
// bounded prompt as a single summary Memory, so if the late keyword is missing
// from the prompt it is unrecallable — proving whether the sampler spans the
// whole session.
func TestIngestReflectsLateSessionContent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	src := scopedSource("codex_session:long", "/work/long")

	const lateKeyword = "zzlatekeyword"
	const eventCount = 100
	const lateIndex = eventCount - 1 // the final event, far past the old 40-event window
	events := make([]sourcespkg.SourceEvent, eventCount)
	for i := range events {
		text := fmt.Sprintf("event number %d filler discussion", i)
		if i == lateIndex {
			text = "we discussed " + lateKeyword + " near the end"
		}
		events[i] = sourcespkg.SourceEvent{
			SourceID:    src.ID,
			Index:       i,
			Line:        i + 1,
			Type:        "response_item",
			Role:        "user",
			Text:        text,
			PayloadJSON: json.RawMessage(`{}`),
			RawJSON:     json.RawMessage(`{}`),
		}
	}
	if err := sources.SaveSource(ctx, src, events); err != nil {
		t.Fatal(err)
	}

	agent := promptEchoAgent(t, "echo")
	if _, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{agent}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{}, time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	got, err := memoriespkg.NewRecaller(memories).Search(ctx, lateKeyword, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recall of late-session keyword %q = %d results, want 1", lateKeyword, len(got))
	}
	if !strings.Contains(got[0].Text, lateKeyword) {
		t.Fatalf("recalled memory text %q missing late keyword %q", got[0].Text, lateKeyword)
	}
}

// promptEchoAgent returns a CommandAgent whose command echoes the ingest prompt
// it receives (the prompt is the last CLI argument), so the prompt's content
// becomes a single summary Memory the test can recall against.
func promptEchoAgent(t *testing.T, name string) memoriespkg.Agent {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return memoriespkg.NewCommandAgent(name, path)
}

func TestIngestSourcesRunsAgentsInParallelAndLinksMemories(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	src := sourcespkg.Source{
		ID:            "codex_session:test",
		Kind:          sourcespkg.SourceKindCodexSession,
		URI:           "memory://test/source",
		ContentSHA256: "hash",
		Scope: sourcespkg.Scope{
			Kind:  sourcespkg.ScopeKindWorkspace,
			Value: "/work/project",
		},
		ImportedAt:   time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		MetadataJSON: json.RawMessage(`{}`),
	}
	events := []sourcespkg.SourceEvent{{
		SourceID:    src.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        "build ingest",
		PayloadJSON: json.RawMessage(`{"text":"build ingest"}`),
		RawJSON:     json.RawMessage(`{"type":"response_item"}`),
	}}
	if err := sources.SaveSource(ctx, src, events); err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 2)
	release := make(chan struct{})
	agents := []memoriespkg.Agent{
		blockingAgent{name: "claude", started: started, release: release},
		blockingAgent{name: "codex", started: started, release: release},
	}

	resultCh := make(chan ingestResult, 1)
	go func() {
		result, err := memoriespkg.NewIngester(sources, memories, agents, nil).
			Ingest(ctx, memoriespkg.IngestOptions{}, time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC))
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
	if run.result.Memories != 2 {
		t.Fatalf("ingested memories = %d, want 2", run.result.Memories)
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("started agents = %#v, want claude and codex", seen)
	}

	recaller := memoriespkg.NewRecaller(memories)
	recalled, err := recaller.SourceMemories(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 2 {
		t.Fatalf("recalled memories length = %d, want 2", len(recalled))
	}

	links, err := recaller.Links(ctx, src.ID)
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
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	first := testSource("codex_session:first", "first")
	second := testSource("codex_session:second", "second")
	if err := sources.SaveSource(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.SaveSource(ctx, second, nil); err != nil {
		t.Fatal(err)
	}

	result, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{staticAgent{name: "fake"}}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{
			SourceIDs: []sourcespkg.SourceID{second.ID},
			Limit:     1,
		}, time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Sources != 1 {
		t.Fatalf("ingested sources = %d, want 1", result.Sources)
	}

	recaller := memoriespkg.NewRecaller(memories)
	firstMemories, err := recaller.SourceMemories(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstMemories) != 0 {
		t.Fatalf("first source memories length = %d, want 0", len(firstMemories))
	}

	secondMemories, err := recaller.SourceMemories(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondMemories) != 1 {
		t.Fatalf("second source memories length = %d, want 1", len(secondMemories))
	}
}

func TestIngestSourcesStoresSuccessfulOutputWhenAnAgentFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	src := testSource("codex_session:test", "test")
	if err := sources.SaveSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}

	result, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{
		staticAgent{name: "ok"},
		failingAgent{name: "pi"},
	}, nil).Ingest(ctx, memoriespkg.IngestOptions{}, time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories != 1 {
		t.Fatalf("ingested memories = %d, want 1", result.Memories)
	}
	if result.Errors != 1 {
		t.Fatalf("agent errors = %d, want 1", result.Errors)
	}

	recalled, err := memoriespkg.NewRecaller(memories).SourceMemories(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 {
		t.Fatalf("source memories length = %d, want 1", len(recalled))
	}
}

func TestIngestReplacesStaleAgentMemoriesOnReingest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	src := testSource("codex_session:test", "test")
	if err := sources.SaveSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	if _, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{staticAgent{name: "x", text: "v1"}}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{staticAgent{name: "x", text: "v2"}}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{}, now); err != nil {
		t.Fatal(err)
	}

	recalled, err := memoriespkg.NewRecaller(memories).SourceMemories(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 {
		t.Fatalf("source memories length = %d, want 1 (old memory not replaced)", len(recalled))
	}
	if recalled[0].Text != "v2" {
		t.Fatalf("memory text = %q, want v2", recalled[0].Text)
	}
}

func TestIngestSkipExistingSkipsAlreadyIngestedAgents(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	src := testSource("codex_session:test", "test")
	if err := sources.SaveSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	if _, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{staticAgent{name: "x", text: "v1"}}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{}, now); err != nil {
		t.Fatal(err)
	}

	// The agent named "x" would fail if it ran, so a clean result proves it was skipped.
	result, err := memoriespkg.NewIngester(sources, memories, []memoriespkg.Agent{failingAgent{name: "x"}}, nil).
		Ingest(ctx, memoriespkg.IngestOptions{SkipExisting: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories != 0 {
		t.Fatalf("ingested memories = %d, want 0", result.Memories)
	}
	if result.Errors != 0 {
		t.Fatalf("agent errors = %d, want 0 (agent should be skipped)", result.Errors)
	}

	recalled, err := memoriespkg.NewRecaller(memories).SourceMemories(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 || recalled[0].Text != "v1" {
		t.Fatalf("source memories = %#v, want single v1 memory", recalled)
	}
}

// TestIngestPassesRelatedMemoryFromSameScopeExcludingOwnSource proves ingest
// recalls existing memory before handing a source to an agent, so the agent
// can connect the new source to what is already known instead of treating it
// as a blank slate — and that a source's own prior memory (a re-ingest) does
// not count as "existing" knowledge about itself.
func TestIngestPassesRelatedMemoryFromSameScopeExcludingOwnSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(dir, "memory.db"))
	defer closeStores()

	const sharedKeyword = "zzsharedtopic"
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)

	seedSrc := scopedSource("codex_session:seed", "/work/project")
	if err := sources.SaveSource(ctx, seedSrc, []sourcespkg.SourceEvent{{
		SourceID:    seedSrc.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        sharedKeyword,
		PayloadJSON: json.RawMessage(`{}`),
		RawJSON:     json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{staticAgent{name: "seed", text: "seed memory about " + sharedKeyword}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{seedSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	// A different scope also mentions the keyword; it must not appear as
	// related for a source ingested under /work/project.
	otherScopeSrc := scopedSource("codex_session:other-scope", "/work/other")
	if err := sources.SaveSource(ctx, otherScopeSrc, []sourcespkg.SourceEvent{{
		SourceID:    otherScopeSrc.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        sharedKeyword + " in a different project",
		PayloadJSON: json.RawMessage(`{}`),
		RawJSON:     json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{staticAgent{name: "seed", text: "unrelated other-scope memory about " + sharedKeyword}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{otherScopeSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	newSrc := scopedSource("codex_session:new", "/work/project")
	if err := sources.SaveSource(ctx, newSrc, []sourcespkg.SourceEvent{{
		SourceID:    newSrc.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        sharedKeyword,
		PayloadJSON: json.RawMessage(`{}`),
		RawJSON:     json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatal(err)
	}

	captured := make(chan memoriespkg.AgentInput, 1)
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{capturingAgent{name: "cap", inputs: captured}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	var input memoriespkg.AgentInput
	select {
	case input = <-captured:
	default:
		t.Fatal("capturing agent was not run")
	}

	if len(input.RelatedMemories) != 1 {
		t.Fatalf("related memories = %#v, want exactly the same-scope seed memory", input.RelatedMemories)
	}
	if !strings.Contains(input.RelatedMemories[0].Text, "seed memory about "+sharedKeyword) {
		t.Fatalf("related memory text = %q, want the same-scope seed memory", input.RelatedMemories[0].Text)
	}

	// Re-ingesting the seed source with a second agent must not see its own
	// prior memory reflected back as "related" — that would just be a self-echo.
	captured2 := make(chan memoriespkg.AgentInput, 1)
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{capturingAgent{name: "cap2", inputs: captured2}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{seedSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}
	select {
	case input = <-captured2:
	default:
		t.Fatal("second capturing agent was not run")
	}
	for _, related := range input.RelatedMemories {
		if strings.Contains(related.Text, "seed memory about "+sharedKeyword) {
			t.Fatalf("related memories = %#v, must not include the source's own prior memory", input.RelatedMemories)
		}
	}
}

// capturingAgent records the AgentInput it receives so a test can assert on
// what ingest computed (such as RelatedMemories) rather than on agent output.
type capturingAgent struct {
	name   string
	inputs chan<- memoriespkg.AgentInput
}

func (a capturingAgent) Name() string {
	return a.name
}

func (a capturingAgent) Ingest(_ context.Context, input memoriespkg.AgentInput) (memoriespkg.AgentOutput, error) {
	a.inputs <- input
	return memoriespkg.AgentOutput{
		Memories: []memoriespkg.AgentMemory{{Kind: memoriespkg.MemoryKindSummary, Text: a.name + " summary"}},
	}, nil
}

func openStores(ctx context.Context, t *testing.T, path string) (*sourcespkg.Store, *memoriespkg.Store, func()) {
	t.Helper()
	return openStoresWith(ctx, t, path, spaceTokenizer{})
}

func openStoresWith(ctx context.Context, t *testing.T, path string, tok memoriespkg.Tokenizer) (*sourcespkg.Store, *memoriespkg.Store, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	closeStore := func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate.Apply(ctx, db, path); err != nil {
		closeStore()
		t.Fatal(err)
	}
	return sourcespkg.NewStore(db), memoriespkg.NewStore(db, tok), closeStore
}

// spaceTokenizer is a deterministic whitespace tokenizer for behavior tests. It
// deliberately does no morphology, so tests that need real Korean tokenization
// use the tokenize package instead.
type spaceTokenizer struct{}

func (spaceTokenizer) Tokenize(text string) []string {
	return strings.Fields(strings.ToLower(text))
}

type ingestResult struct {
	result memoriespkg.IngestResult
	err    error
}

func testSource(id sourcespkg.SourceID, text string) sourcespkg.Source {
	return sourcespkg.Source{
		ID:            id,
		Kind:          sourcespkg.SourceKindCodexSession,
		URI:           "memory://test/" + text,
		ContentSHA256: "hash-" + text,
		Scope: sourcespkg.Scope{
			Kind:  sourcespkg.ScopeKindWorkspace,
			Value: "/work/project",
		},
		ImportedAt:   time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
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

func (a staticAgent) Ingest(context.Context, memoriespkg.AgentInput) (memoriespkg.AgentOutput, error) {
	text := a.text
	if text == "" {
		text = "static summary"
	}
	return memoriespkg.AgentOutput{
		Memories: []memoriespkg.AgentMemory{{
			Kind: memoriespkg.MemoryKindSummary,
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

func (a failingAgent) Ingest(context.Context, memoriespkg.AgentInput) (memoriespkg.AgentOutput, error) {
	return memoriespkg.AgentOutput{}, assertAnError{}
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

func (a blockingAgent) Ingest(ctx context.Context, input memoriespkg.AgentInput) (memoriespkg.AgentOutput, error) {
	select {
	case a.started <- a.name:
	case <-ctx.Done():
		return memoriespkg.AgentOutput{}, ctx.Err()
	}

	select {
	case <-a.release:
	case <-ctx.Done():
		return memoriespkg.AgentOutput{}, ctx.Err()
	}

	return memoriespkg.AgentOutput{
		Memories: []memoriespkg.AgentMemory{{
			Kind: memoriespkg.MemoryKindSummary,
			Text: a.name + " summary for " + string(input.Source.ID),
		}},
	}, nil
}
