package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/source"
)

// hookEvent is the subset of a Claude Code hook payload we read from stdin. The
// harness delivers a JSON object on stdin for lifecycle events (Stop,
// SessionEnd, ...); transcript_path points at the session's JSONL file.
type hookEvent struct {
	TranscriptPath string `json:"transcript_path"`
	SessionID      string `json:"session_id"`
}

// runHook records the session named by a Claude Code hook payload read from
// stdin, so finished sessions become recallable Memory sources automatically.
// It is deliberately fail-soft: any problem is reported to stderr but returns
// nil, so a hook never breaks the user's Claude Code session. Ingest (the LLM
// step) is left to `my memory ingest` batch runs to keep the hook fast.
func runHook(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, now time.Time) error {
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storePath := flags.String("store", "", "SQLite memory store path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *storePath == "" {
		defaultPath, err := defaultStorePath()
		if err != nil {
			return err
		}
		*storePath = defaultPath
	}

	var event hookEvent
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return note(stderr, "hook: ignoring unreadable payload: %v", err)
	}
	if event.TranscriptPath == "" {
		return note(stderr, "hook: payload has no transcript_path; nothing to import")
	}

	if err := withStores(ctx, *storePath, func(sources *source.Store, _ *memory.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		src, readErr := source.NewImporter(sources, logger).Read(ctx, event.TranscriptPath, now)
		if readErr != nil {
			// Fail-soft: an unsupported or unreadable transcript must not fail the hook.
			return note(stderr, "hook: skipped %s: %v", event.TranscriptPath, readErr)
		}
		return note(stdout, "recorded session %s", src.ID)
	}); err != nil {
		return note(stderr, "hook: %v", err)
	}
	return nil
}

// note writes a one-line diagnostic and returns only a write error, so callers
// can `return note(...)` to stay fail-soft (transcript problems become nil) while
// still satisfying errcheck on the write itself.
func note(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format+"\n", args...)
	return err
}
