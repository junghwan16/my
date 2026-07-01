package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	"github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
)

func TestMemoryImportFromDirectoryRecordsSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	codexPath := filepath.Join(sessionsDir, "codex", "rollout.jsonl")
	claudePath := filepath.Join(sessionsDir, "claude", "session.jsonl")
	dbPath := filepath.Join(dir, "memory.db")
	now := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)

	writeFile(t, codexPath, strings.Join([]string{
		`{"timestamp":"2026-06-30T01:39:24.928Z","type":"session_meta","payload":{"id":"session-1","session_id":"session-1","timestamp":"2026-06-30T01:38:24.005Z","cwd":"/work/project"}}`,
		`{"timestamp":"2026-06-30T01:39:31.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	}, "\n"))
	writeFile(t, claudePath, strings.Join([]string{
		`{"type":"mode","mode":"normal","sessionId":"claude-session"}`,
		`{"type":"user","message":{"role":"user","content":"debug this"},"timestamp":"2026-06-25T02:42:26.463Z","cwd":"/work/claude","sessionId":"claude-session"}`,
	}, "\n"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(ctx, []string{"memory", "import", "--from", sessionsDir, "--store", dbPath}, &stdout, &stderr, now)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}

	sources, _, closeStores := openStores(ctx, t, dbPath)
	defer closeStores()

	if !strings.Contains(stdout.String(), "imported 2 session") {
		t.Fatalf("stdout = %q, want import count", stdout.String())
	}

	recorded, err := sources.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 2 {
		t.Fatalf("sources length = %d, want 2", len(recorded))
	}
}

func TestMemoryImportUsesDefaultStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	sessionsDir := filepath.Join(dir, "sessions")
	sessionPath := filepath.Join(sessionsDir, "rollout.jsonl")
	dbPath := filepath.Join(dir, ".local", "share", "gieok", "memory", "gieok.db")
	now := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)

	writeFile(t, sessionPath, `{"timestamp":"2026-06-30T01:39:24.928Z","type":"session_meta","payload":{"id":"session-1","session_id":"session-1","timestamp":"2026-06-30T01:38:24.005Z","cwd":"/work/project"}}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(ctx, []string{"memory", "import", "--from", sessionsDir}, &stdout, &stderr, now)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}

	sources, _, closeStores := openStores(ctx, t, dbPath)
	defer closeStores()

	recorded, err := sources.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 {
		t.Fatalf("sources length = %d, want 1", len(recorded))
	}
}

func TestMemoryIngestRunsConfiguredAgent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	agentPath := filepath.Join(dir, "fake-agent")
	now := time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC)

	writeFile(t, agentPath, "#!/bin/sh\nprintf 'summary from fake: %s' \"$*\"\n")
	if err := os.Chmod(agentPath, 0o700); err != nil {
		t.Fatal(err)
	}

	sources, _, closeStores := openStores(ctx, t, dbPath)
	src := cliTestSource("codex_session:test", "source", now)
	if err := sources.RecordSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	closeStores()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(ctx, []string{"memory", "ingest", "--store", dbPath, "--agent", "fake=" + agentPath}, &stdout, &stderr, now)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ingested 1 source") {
		t.Fatalf("stdout = %q, want ingest count", stdout.String())
	}

	_, memories, closeStores := openStores(ctx, t, dbPath)
	defer closeStores()

	recalled, err := memory.NewRecaller(memories).Recall(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 {
		t.Fatalf("source memories length = %d, want 1", len(recalled))
	}
	if !strings.Contains(recalled[0].Text, "memory://test/source") {
		t.Fatalf("memory text = %q, want source URI", recalled[0].Text)
	}
}

func TestMemoryIngestCanLimitSources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	agentPath := filepath.Join(dir, "fake-agent")
	now := time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC)

	writeFile(t, agentPath, "#!/bin/sh\nprintf 'summary from fake: %s' \"$*\"\n")
	if err := os.Chmod(agentPath, 0o700); err != nil {
		t.Fatal(err)
	}

	sources, _, closeStores := openStores(ctx, t, dbPath)
	first := cliTestSource("codex_session:first", "first", now)
	second := cliTestSource("codex_session:second", "second", now)
	if err := sources.RecordSource(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.RecordSource(ctx, second, nil); err != nil {
		t.Fatal(err)
	}
	closeStores()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		ctx,
		[]string{"memory", "ingest", "--store", dbPath, "--agent", "fake=" + agentPath, "--limit", "1"},
		&stdout,
		&stderr,
		now,
	)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}

	_, memories, closeStores := openStores(ctx, t, dbPath)
	defer closeStores()
	recaller := memory.NewRecaller(memories)
	firstMemories, err := recaller.Recall(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstMemories) != 1 {
		t.Fatalf("first source memories length = %d, want 1", len(firstMemories))
	}
	secondMemories, err := recaller.Recall(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondMemories) != 0 {
		t.Fatalf("second source memories length = %d, want 0", len(secondMemories))
	}
}

func TestMemoryIngestDefaultsToLocalAgents(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	config, err := parseMemoryIngestConfig([]string{"memory", "ingest", "--store", dbPath}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.agents) != 3 {
		t.Fatalf("agents length = %d, want 3", len(config.agents))
	}
	if config.agents[0].Name() != "claude" {
		t.Fatalf("first agent = %q, want claude", config.agents[0].Name())
	}
	if config.agents[1].Name() != "codex" {
		t.Fatalf("second agent = %q, want codex", config.agents[1].Name())
	}
	if config.agents[2].Name() != "pi" {
		t.Fatalf("third agent = %q, want pi", config.agents[2].Name())
	}
}

func TestMemoryIngestParsesTuningFlags(t *testing.T) {
	config, err := parseMemoryIngestConfig(
		[]string{"memory", "ingest", "--store", "/tmp/x.db", "--concurrency", "2", "--skip-existing"},
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.Concurrency != 2 {
		t.Fatalf("concurrency = %d, want 2", config.options.Concurrency)
	}
	if !config.options.SkipExisting {
		t.Fatal("skip-existing = false, want true")
	}
}

func cliTestSource(id source.SourceID, name string, now time.Time) source.Source {
	return source.Source{
		ID:            id,
		Kind:          source.SourceKindCodexSession,
		URI:           "memory://test/" + name,
		ContentSHA256: "hash-" + name,
		Scope: source.Scope{
			Kind:  source.ScopeKindWorkspace,
			Value: "/work/project",
		},
		RecordedAt:   now,
		MetadataJSON: json.RawMessage(`{}`),
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStores(ctx context.Context, t *testing.T, path string) (*source.Store, *memory.Store, func()) {
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
	return source.NewStore(db), memory.NewStore(db, spaceTokenizer{}), closeStore
}

// spaceTokenizer is a whitespace tokenizer used only to satisfy NewStore in
// these CLI tests, which read back memory rather than searching it.
type spaceTokenizer struct{}

func (spaceTokenizer) Tokenize(text string) []string {
	return strings.Fields(strings.ToLower(text))
}
