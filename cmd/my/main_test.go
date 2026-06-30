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

	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/storage"
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

	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	if !strings.Contains(stdout.String(), "imported 2 session") {
		t.Fatalf("stdout = %q, want import count", stdout.String())
	}

	sources, err := store.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources length = %d, want 2", len(sources))
	}
}

func TestMemoryImportUsesDefaultStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	sessionsDir := filepath.Join(dir, "sessions")
	sessionPath := filepath.Join(sessionsDir, "rollout.jsonl")
	dbPath := filepath.Join(dir, ".local", "share", "my", "memory", "my.db")
	now := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)

	writeFile(t, sessionPath, `{"timestamp":"2026-06-30T01:39:24.928Z","type":"session_meta","payload":{"id":"session-1","session_id":"session-1","timestamp":"2026-06-30T01:38:24.005Z","cwd":"/work/project"}}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(ctx, []string{"memory", "import", "--from", sessionsDir}, &stdout, &stderr, now)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}

	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	sources, err := store.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources length = %d, want 1", len(sources))
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

	store, closeStore := openStore(ctx, t, dbPath)
	source := memory.Source{
		ID:            "codex_session:test",
		Kind:          memory.SourceKindCodexSession,
		URI:           "memory://test/source",
		ContentSHA256: "hash",
		Scope: memory.Scope{
			Kind:  memory.ScopeKindWorkspace,
			Value: "/work/project",
		},
		RecordedAt:   now,
		MetadataJSON: json.RawMessage(`{}`),
	}
	if err := store.RecordSource(ctx, source, nil); err != nil {
		t.Fatal(err)
	}
	closeStore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(ctx, []string{"memory", "ingest", "--store", dbPath, "--agent", "fake=" + agentPath}, &stdout, &stderr, now)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ingested 1 source") {
		t.Fatalf("stdout = %q, want ingest count", stdout.String())
	}

	store, closeStore = openStore(ctx, t, dbPath)
	defer closeStore()

	items, err := store.SourceItems(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("source items length = %d, want 1", len(items))
	}
	if !strings.Contains(items[0].Text, "memory://test/source") {
		t.Fatalf("item text = %q, want source URI", items[0].Text)
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

	store, closeStore := openStore(ctx, t, dbPath)
	first := cliTestSource("codex_session:first", "first", now)
	second := cliTestSource("codex_session:second", "second", now)
	if err := store.RecordSource(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSource(ctx, second, nil); err != nil {
		t.Fatal(err)
	}
	closeStore()

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

	store, closeStore = openStore(ctx, t, dbPath)
	defer closeStore()
	firstItems, err := store.SourceItems(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstItems) != 1 {
		t.Fatalf("first source items length = %d, want 1", len(firstItems))
	}
	secondItems, err := store.SourceItems(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondItems) != 0 {
		t.Fatalf("second source items length = %d, want 0", len(secondItems))
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

func cliTestSource(id memory.SourceID, name string, now time.Time) memory.Source {
	return memory.Source{
		ID:            id,
		Kind:          memory.SourceKindCodexSession,
		URI:           "memory://test/" + name,
		ContentSHA256: "hash-" + name,
		Scope: memory.Scope{
			Kind:  memory.ScopeKindWorkspace,
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

func openStore(ctx context.Context, t *testing.T, path string) (*memory.Store, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Migrate(ctx, db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("migrate sqlite database: %v; close sqlite database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	return memory.NewStore(db), func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
