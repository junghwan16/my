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

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
	"github.com/junghwan16/gieok/internal/tokenize"
)

func TestMemoryRecallReturnsRelevantMemoryInScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/project", "memory:a", "코스피 종목 분석 리포트")
	seedMemory(ctx, t, dbPath, "codex_session:b", "/work/project", "memory:b", "오늘 날씨 정보")

	stdout, stderr := runRecall(ctx, t, dbPath, "종목", "--scope", "/work/project")

	// Hybrid recall fuses lexical and (when Ollama is up) semantic rankings, so
	// the lexically matching memory:a must rank first and carry its source
	// context. memory:b may or may not appear depending on whether a semantic
	// engine is attached, but it must never outrank the lexical match.
	if !strings.Contains(stdout, "memory:a") {
		t.Fatalf("stdout = %q, want memory:a\nstderr: %s", stdout, stderr)
	}
	aPos := strings.Index(stdout, "memory:a")
	bPos := strings.Index(stdout, "memory:b")
	if bPos >= 0 && bPos < aPos {
		t.Fatalf("stdout = %q, want lexical match memory:a ranked above memory:b", stdout)
	}
	if !strings.Contains(stdout, "from source codex_session:a") || !strings.Contains(stdout, "scope=/work/project") {
		t.Fatalf("stdout = %q, want source context", stdout)
	}
}

func TestMemoryRecallFiltersByScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/a", "memory:a", "종목 분석")
	seedMemory(ctx, t, dbPath, "codex_session:b", "/work/b", "memory:b", "종목 추천")

	scoped, _ := runRecall(ctx, t, dbPath, "종목", "--scope", "/work/a")
	if !strings.Contains(scoped, "memory:a") || strings.Contains(scoped, "memory:b") {
		t.Fatalf("scoped recall = %q, want only memory:a", scoped)
	}

	all, _ := runRecall(ctx, t, dbPath, "종목", "--all-scopes")
	if !strings.Contains(all, "memory:a") || !strings.Contains(all, "memory:b") {
		t.Fatalf("all-scopes recall = %q, want both memories", all)
	}
}

func TestMemoryRecallRespectsLimit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/a", "memory:a", "종목 하나")
	seedMemory(ctx, t, dbPath, "codex_session:b", "/work/a", "memory:b", "종목 둘")
	seedMemory(ctx, t, dbPath, "codex_session:c", "/work/a", "memory:c", "종목 셋")

	stdout, _ := runRecall(ctx, t, dbPath, "종목", "--scope", "/work/a", "--limit", "2")
	if !strings.Contains(stdout, "found 2 memory") {
		t.Fatalf("stdout = %q, want limit honored", stdout)
	}
}

func TestMemoryRecallWithoutTaskReturnsRecentMemoryInScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	seedMemoryAt(ctx, t, dbPath, "codex_session:old", "/work/a", "memory:old", "예전 기록", time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	seedMemoryAt(ctx, t, dbPath, "codex_session:new", "/work/a", "memory:new", "최근 기록", time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	seedMemoryAt(ctx, t, dbPath, "codex_session:other", "/work/b", "memory:other", "다른 스코프", time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))

	stdout, _ := runRecall(ctx, t, dbPath, "", "--scope", "/work/a", "--limit", "1")
	if !strings.Contains(stdout, "found 1 memory") || !strings.Contains(stdout, "memory:new") {
		t.Fatalf("recent recall = %q, want most recent in-scope memory:new", stdout)
	}
	if strings.Contains(stdout, "memory:other") {
		t.Fatalf("recent recall = %q, want out-of-scope memory excluded", stdout)
	}
}

func TestMemoryRecallEmptyResultIsExplicit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	// No memory in the queried scope at all, so both the lexical and the
	// semantic ranker return nothing and recall reports the empty result
	// explicitly — regardless of whether a semantic engine is attached.
	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/a", "memory:a", "종목 분석")

	stdout, _ := runRecall(ctx, t, dbPath, "날씨", "--scope", "/work/empty")
	if !strings.Contains(stdout, "no matching memory") {
		t.Fatalf("stdout = %q, want explicit empty result", stdout)
	}
}

func TestMemoryRecallJSONCarriesSemanticFields(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/project", "memory:a", "코스피 종목 분석")

	stdout, _ := runRecall(ctx, t, dbPath, "종목", "--scope", "/work/project", "--json")

	var result struct {
		Memories []struct {
			MemoryID  string `json:"memory_id"`
			Agent     string `json:"agent"`
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			CreatedAt string `json:"created_at"`
			Sources   []struct {
				ID    string `json:"id"`
				URI   string `json:"uri"`
				Scope struct {
					Kind  string `json:"kind"`
					Value string `json:"value"`
				} `json:"scope"`
			} `json:"sources"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("json output not parseable: %v\noutput: %s", err, stdout)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("json memories = %d, want 1", len(result.Memories))
	}
	got := result.Memories[0]
	if got.MemoryID != "memory:a" || got.Agent != "t" || got.Kind != string(memoriespkg.MemoryKindSummary) {
		t.Fatalf("json memory identity = %+v, want memory:a/t/summary", got)
	}
	if got.CreatedAt == "" || got.Text == "" {
		t.Fatalf("json memory missing text/created: %+v", got)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("json sources = %d, want 1", len(got.Sources))
	}
	src := got.Sources[0]
	if src.ID != "codex_session:a" || src.Scope.Value != "/work/project" || src.URI == "" {
		t.Fatalf("json source context = %+v, want linked source", src)
	}
}

func TestMemoryRecallEmptyJSONIsArray(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	// Query an empty scope so both rankers return nothing and the JSON output
	// is an empty array rather than null — independent of any semantic engine.
	seedMemory(ctx, t, dbPath, "codex_session:a", "/work/a", "memory:a", "종목 분석")

	stdout, _ := runRecall(ctx, t, dbPath, "날씨", "--scope", "/work/empty", "--json")
	var result struct {
		Memories []json.RawMessage `json:"memories"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("empty json not parseable: %v\noutput: %s", err, stdout)
	}
	if result.Memories == nil {
		t.Fatalf("empty json memories = null, want []")
	}
	if len(result.Memories) != 0 {
		t.Fatalf("empty json memories = %d, want 0", len(result.Memories))
	}
}

func TestMemoryRecallRejectsConflictingTaskInput(t *testing.T) {
	_, err := parseMemoryRecallConfig(
		[]string{"memory", "recall", "종목", "--task", "종목", "--store", "/tmp/x.db"},
		io.Discard,
	)
	if err == nil {
		t.Fatal("want error when task given both positionally and via --task")
	}
	if !strings.Contains(err.Error(), "positionally or with --task") {
		t.Fatalf("error = %q, want actionable task-conflict message", err)
	}
}

func TestMemoryRecallRejectsConflictingScopeInput(t *testing.T) {
	_, err := parseMemoryRecallConfig(
		[]string{"memory", "recall", "종목", "--scope", "/work/a", "--all-scopes"},
		io.Discard,
	)
	if err == nil {
		t.Fatal("want error when --scope and --all-scopes combined")
	}
	if !strings.Contains(err.Error(), "--scope or --all-scopes") {
		t.Fatalf("error = %q, want actionable scope-conflict message", err)
	}
}

func TestMemoryRecallDefaultsToCurrentWorkspaceScope(t *testing.T) {
	config, err := parseMemoryRecallConfig(
		[]string{"memory", "recall", "종목", "--store", "/tmp/x.db"},
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if config.scope != cwd {
		t.Fatalf("default scope = %q, want cwd %q", config.scope, cwd)
	}
}

func runRecall(ctx context.Context, t *testing.T, dbPath, task string, extra ...string) (string, string) {
	t.Helper()
	args := []string{"memory", "recall"}
	if task != "" {
		args = append(args, task)
	}
	args = append(args, "--store", dbPath)
	args = append(args, extra...)

	var stdout, stderr bytes.Buffer
	if err := run(ctx, args, &stdout, &stderr, time.Time{}); err != nil {
		t.Fatalf("recall run returned error: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func seedMemory(ctx context.Context, t *testing.T, dbPath string, srcID sourcespkg.SourceID, scope, memID, text string) {
	t.Helper()
	seedMemoryAt(ctx, t, dbPath, srcID, scope, memID, text, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
}

func seedMemoryAt(ctx context.Context, t *testing.T, dbPath string, srcID sourcespkg.SourceID, scope, memID, text string, at time.Time) {
	t.Helper()
	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	if err = migrate.Apply(ctx, db, dbPath); err != nil {
		t.Fatal(err)
	}
	tok, err := tokenize.NewKorean()
	if err != nil {
		t.Fatal(err)
	}
	sources := sourcespkg.NewStore(db)
	memories := memoriespkg.NewStore(db, tok)

	src := sourcespkg.Source{
		ID:            srcID,
		Kind:          sourcespkg.SourceKindCodexSession,
		URI:           "memory://test/" + string(srcID),
		ContentSHA256: "hash-" + string(srcID),
		Scope:         sourcespkg.Scope{Kind: sourcespkg.ScopeKindWorkspace, Value: scope},
		ImportedAt:    at,
		MetadataJSON:  json.RawMessage(`{}`),
	}
	if err := sources.SaveSource(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	mem := memoriespkg.Memory{
		ID:           memoriespkg.MemoryID(memID),
		Agent:        "t",
		Kind:         memoriespkg.MemoryKindSummary,
		Text:         text,
		CreatedAt:    at,
		MetadataJSON: json.RawMessage(`{}`),
	}
	link := memoriespkg.Link{
		SourceID:     src.ID,
		MemoryID:     mem.ID,
		Kind:         memoriespkg.LinkKindSourceIngest,
		CreatedAt:    at,
		MetadataJSON: json.RawMessage(`{}`),
	}
	if err := memories.ReplaceSourceMemories(ctx, src.ID, "t", []memoriespkg.Memory{mem}, []memoriespkg.Link{link}, nil); err != nil {
		t.Fatal(err)
	}
}
