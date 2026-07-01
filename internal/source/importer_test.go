package source_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/junghwan16/my/internal/migrate"
	"github.com/junghwan16/my/internal/source"
	"github.com/junghwan16/my/internal/storage"
)

func TestImporterReadCodexSessionMakesSourceRecallable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	sessionPath := filepath.Join(dir, "rollout.jsonl")
	now := time.Date(2026, 7, 1, 10, 30, 0, 0, time.UTC)

	writeFile(t, sessionPath, strings.Join([]string{
		`{"timestamp":"2026-06-30T01:39:24.928Z","type":"session_meta","payload":{"id":"session-1","session_id":"session-1","timestamp":"2026-06-30T01:38:24.005Z","cwd":"/work/project","originator":"codex-tui","cli_version":"0.142.1","source":"cli","thread_source":"user","model_provider":"openai"}}`,
		`{"timestamp":"2026-06-30T01:39:30.000Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/work/project","current_date":"2026-06-30","timezone":"Asia/Seoul","model":"gpt-5"}}`,
		`{"timestamp":"2026-06-30T01:39:31.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"import codex sessions"}]}}`,
	}, "\n"))

	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	src, err := source.NewImporter(store, nil).Read(ctx, sessionPath, now)
	if err != nil {
		t.Fatal(err)
	}

	if src.ID == "" {
		t.Fatal("source ID is empty")
	}
	if src.Kind != source.SourceKindCodexSession {
		t.Fatalf("source kind = %q, want %q", src.Kind, source.SourceKindCodexSession)
	}
	if src.URI != sessionPath {
		t.Fatalf("source URI = %q, want %q", src.URI, sessionPath)
	}
	if src.Scope.Kind != source.ScopeKindWorkspace {
		t.Fatalf("source scope kind = %q, want %q", src.Scope.Kind, source.ScopeKindWorkspace)
	}
	if src.Scope.Value != "/work/project" {
		t.Fatalf("source scope value = %q, want /work/project", src.Scope.Value)
	}
	if !src.RecordedAt.Equal(now) {
		t.Fatalf("source recorded at = %s, want %s", src.RecordedAt, now)
	}
	if got, want := src.StartedAt, time.Date(2026, 6, 30, 1, 38, 24, 5_000_000, time.UTC); !got.Equal(want) {
		t.Fatalf("source started at = %s, want %s", got, want)
	}
	if !strings.Contains(string(src.MetadataJSON), `"session_id":"session-1"`) {
		t.Fatalf("source metadata = %s, want session id", src.MetadataJSON)
	}

	storedSource, err := store.Source(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSource.ContentSHA256 == "" {
		t.Fatal("stored source content hash is empty")
	}

	events, err := store.SourceEvents(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("source events length = %d, want 3", len(events))
	}
	if events[2].Type != "response_item" {
		t.Fatalf("third event type = %q, want response_item", events[2].Type)
	}
	if events[2].Role != "user" {
		t.Fatalf("third event role = %q, want user", events[2].Role)
	}
	if events[2].Text != "import codex sessions" {
		t.Fatalf("third event text = %q, want import codex sessions", events[2].Text)
	}
	if !json.Valid(events[2].PayloadJSON) {
		t.Fatal("third event payload is not valid JSON")
	}
	if !json.Valid(events[2].RawJSON) {
		t.Fatal("third event raw JSON is not valid JSON")
	}
}

func TestImporterReadClaudeCodeSessionMakesSourceRecallable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	sessionPath := filepath.Join(dir, "claude.jsonl")
	now := time.Date(2026, 7, 1, 10, 30, 0, 0, time.UTC)

	writeFile(t, sessionPath, strings.Join([]string{
		`{"type":"mode","mode":"normal","sessionId":"claude-session"}`,
		`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"debug this"},"uuid":"u1","timestamp":"2026-06-25T02:42:26.463Z","cwd":"/work/claude","sessionId":"claude-session","version":"2.1.186"}`,
		`{"parentUuid":"u1","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"checking"}]},"uuid":"a1","timestamp":"2026-06-25T02:42:32.681Z","cwd":"/work/claude","sessionId":"claude-session","version":"2.1.186"}`,
	}, "\n"))

	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	src, err := source.NewImporter(store, nil).Read(ctx, sessionPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != source.SourceKindClaudeCodeSession {
		t.Fatalf("source kind = %q, want %q", src.Kind, source.SourceKindClaudeCodeSession)
	}
	if src.Scope.Value != "/work/claude" {
		t.Fatalf("source scope value = %q, want /work/claude", src.Scope.Value)
	}
	if got, want := src.StartedAt, time.Date(2026, 6, 25, 2, 42, 26, 463_000_000, time.UTC); !got.Equal(want) {
		t.Fatalf("source started at = %s, want %s", got, want)
	}
	if !strings.Contains(string(src.MetadataJSON), `"session_id":"claude-session"`) {
		t.Fatalf("source metadata = %s, want claude session id", src.MetadataJSON)
	}

	events, err := store.SourceEvents(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("source events length = %d, want 3", len(events))
	}
	if events[0].Type != "mode" {
		t.Fatalf("first event type = %q, want mode", events[0].Type)
	}
	if events[1].Type != "user" {
		t.Fatalf("second event type = %q, want user", events[1].Type)
	}
	if events[1].Text != "debug this" {
		t.Fatalf("second event text = %q, want debug this", events[1].Text)
	}
	if events[2].Type != "assistant" {
		t.Fatalf("third event type = %q, want assistant", events[2].Type)
	}
	if events[2].Text != "checking" {
		t.Fatalf("third event text = %q, want checking", events[2].Text)
	}
}

func TestImporterImportSkipsUnsupportedFilesInDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	sessionsDir := filepath.Join(dir, "sessions")
	now := time.Date(2026, 7, 1, 10, 30, 0, 0, time.UTC)

	writeFile(t, filepath.Join(sessionsDir, "current.jsonl"), `{"timestamp":"2026-06-30T01:39:24.928Z","type":"session_meta","payload":{"id":"session-1","session_id":"session-1","timestamp":"2026-06-30T01:38:24.005Z","cwd":"/work/project"}}`)
	writeFile(t, filepath.Join(sessionsDir, "legacy.jsonl"), `{"id":"legacy-session","timestamp":"2025-09-10T14:24:38.268Z","instructions":null}`)

	store, closeStore := openStore(ctx, t, dbPath)
	defer closeStore()

	result, err := source.NewImporter(store, nil).Import(ctx, sessionsDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("imported = %d, want 1", result.Imported)
	}
	if result.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", result.Skipped)
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

func openStore(ctx context.Context, t *testing.T, path string) (*source.Store, func()) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, path); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("apply migrations: %v; close sqlite database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	return source.NewStore(db), func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
