package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SourceRecorder stores a parsed source and its normalized events.
type SourceRecorder interface {
	RecordSource(context.Context, Source, []SourceEvent) error
}

type sourceFunc func(string, []byte, time.Time) (Source, []SourceEvent, error)

// ImportResult summarizes a session import run.
type ImportResult struct {
	Imported int
	Skipped  int
	Sources  []Source
}

var errUnsupportedSession = errors.New("unsupported session file")

// ImportSessions imports all supported JSONL session files from a file or directory.
func ImportSessions(
	ctx context.Context,
	recorder SourceRecorder,
	from string,
	now time.Time,
	logger *slog.Logger,
) (ImportResult, error) {
	logger = loggerOrDiscard(logger)

	info, err := os.Stat(from)
	if err != nil {
		return ImportResult{}, fmt.Errorf("stat sessions path: %w", err)
	}
	if !info.IsDir() {
		source, recordErr := RecordSessionFile(ctx, recorder, from, now)
		if recordErr != nil {
			return ImportResult{}, recordErr
		}
		logger.InfoContext(ctx, "imported session source",
			"path", from,
			"kind", source.Kind,
			"source_id", source.ID,
		)
		return ImportResult{Imported: 1, Sources: []Source{source}}, nil
	}

	var result ImportResult
	err = filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		source, recordErr := RecordSessionFile(ctx, recorder, path, now)
		if recordErr != nil {
			if errors.Is(recordErr, errUnsupportedSession) {
				result.Skipped++
				logger.InfoContext(ctx, "skipped unsupported session file", "path", path)
				return nil
			}
			return recordErr
		}
		result.Imported++
		result.Sources = append(result.Sources, source)
		logger.InfoContext(ctx, "imported session source",
			"path", path,
			"kind", source.Kind,
			"source_id", source.ID,
		)
		return nil
	})
	if err != nil {
		return ImportResult{}, fmt.Errorf("walk sessions path: %w", err)
	}
	return result, nil
}

// RecordSessionFile parses and records one supported session file.
func RecordSessionFile(ctx context.Context, recorder SourceRecorder, path string, now time.Time) (Source, error) {
	//nolint:gosec // Import reads a user-selected session file path.
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read session file: %w", err)
	}

	source, events, err := parseSessionSource(path, raw, now)
	if err != nil {
		return Source{}, err
	}

	if err := recorder.RecordSource(ctx, source, events); err != nil {
		return Source{}, err
	}
	return source, nil
}

func parseSessionSource(path string, raw []byte, now time.Time) (Source, []SourceEvent, error) {
	for _, source := range []sourceFunc{
		codexSource,
		claudeSource,
	} {
		source, events, err := source(path, raw, now)
		if err == nil {
			return source, events, nil
		}
		if !errors.Is(err, errUnsupportedSession) {
			return Source{}, nil, err
		}
	}
	return Source{}, nil, fmt.Errorf("%w: %s", errUnsupportedSession, path)
}

func firstJSONLRecord(raw []byte) []byte {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		record := bytes.TrimSpace(line)
		if len(record) > 0 {
			return record
		}
	}
	return nil
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}

// firstEventTime returns the timestamp of the earliest timestamped event.
func firstEventTime(events []SourceEvent) time.Time {
	for _, event := range events {
		if !event.At.IsZero() {
			return event.At
		}
	}
	return time.Time{}
}

// lastEventTime returns the timestamp of the latest timestamped event.
func lastEventTime(events []SourceEvent) time.Time {
	for i := len(events) - 1; i >= 0; i-- {
		if !events[i].At.IsZero() {
			return events[i].At
		}
	}
	return time.Time{}
}
