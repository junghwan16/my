package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookImportsTranscriptFromPayload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	sessionPath := filepath.Join(dir, "session.jsonl")
	now := time.Date(2026, 7, 1, 14, 0, 0, 0, time.UTC)

	writeFile(t, sessionPath, strings.Join([]string{
		`{"type":"mode","mode":"normal","sessionId":"s1"}`,
		`{"type":"user","message":{"role":"user","content":"종목 분석 부탁해"},"timestamp":"2026-06-25T02:42:26.463Z","cwd":"/work/claude","sessionId":"s1"}`,
	}, "\n"))

	payload, err := json.Marshal(hookEvent{TranscriptPath: sessionPath, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err = runHook(ctx, []string{"hook", "--store", dbPath}, bytes.NewReader(payload), &stdout, &stderr, now); err != nil {
		t.Fatalf("runHook returned error: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recorded session") {
		t.Fatalf("stdout = %q, want recorded confirmation", stdout.String())
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

func TestHookFailsSoftOnBadPayload(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.db")

	var stdout, stderr bytes.Buffer
	// A hook must never break the session: malformed stdin returns nil, imports nothing.
	if err := runHook(ctx, []string{"hook", "--store", dbPath}, strings.NewReader("not json"), &stdout, &stderr, time.Now()); err != nil {
		t.Fatalf("runHook returned error on bad payload: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing imported", stdout.String())
	}
}

func TestHookFailsSoftOnMissingTranscript(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	payload, err := json.Marshal(hookEvent{TranscriptPath: filepath.Join(dir, "does-not-exist.jsonl")})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err = runHook(ctx, []string{"hook", "--store", dbPath}, bytes.NewReader(payload), &stdout, &stderr, time.Now()); err != nil {
		t.Fatalf("runHook returned error on missing transcript: %v", err)
	}

	sources, _, closeStores := openStores(ctx, t, dbPath)
	defer closeStores()
	recorded, err := sources.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Fatalf("sources length = %d, want 0 (nothing imported)", len(recorded))
	}
}
