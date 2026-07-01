package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Recorder stores a parsed source and its normalized events. *Store satisfies it.
type Recorder interface {
	RecordSource(context.Context, Source, []SourceEvent) error
}

// SessionFormat recognizes and parses one coding-agent session file format.
// Implementations declare only what is format-specific (how to recognize a file
// and how to turn its bytes into events plus metadata); the shared Source
// envelope is assembled once in assembleSource.
type SessionFormat interface {
	// Kind is the SourceKind this format produces. It also prefixes the Source ID.
	Kind() SourceKind
	// Sniff reports whether raw looks like this format.
	Sniff(raw []byte) bool
	// Parse turns raw session bytes into ordered events and format metadata.
	Parse(raw []byte) (ParsedSession, error)
}

// ParsedSession is everything a SessionFormat extracts from a session file. The
// shared envelope (content hash, ID, URI, recorded-at, ended-at, event indexing)
// is filled in by assembleSource, not by the format.
type ParsedSession struct {
	Events    []SourceEvent
	Scope     Scope
	StartedAt time.Time
	Metadata  json.RawMessage
}

// sessionFormats is the ordered set of recognized session formats. The first one
// whose Sniff matches wins.
var sessionFormats = []SessionFormat{
	codexFormat{},
	claudeFormat{},
}

// ImportResult summarizes a session import run.
type ImportResult struct {
	Imported int
	Skipped  int
	Sources  []Source
}

var errUnsupportedSession = errors.New("unsupported session file")

// Importer reads coding-agent session files and records them as sources.
type Importer struct {
	store  Recorder
	logger *slog.Logger
}

// NewImporter returns an Importer that records into store.
func NewImporter(store Recorder, logger *slog.Logger) *Importer {
	return &Importer{store: store, logger: loggerOrDiscard(logger)}
}

// Import records every supported JSONL session file under a file or directory.
func (im *Importer) Import(ctx context.Context, from string, now time.Time) (ImportResult, error) {
	info, err := os.Stat(from)
	if err != nil {
		return ImportResult{}, fmt.Errorf("stat sessions path: %w", err)
	}
	if !info.IsDir() {
		source, recordErr := im.Read(ctx, from, now)
		if recordErr != nil {
			return ImportResult{}, recordErr
		}
		im.logImported(ctx, from, source)
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
		source, recordErr := im.Read(ctx, path, now)
		if recordErr != nil {
			if errors.Is(recordErr, errUnsupportedSession) {
				result.Skipped++
				im.logger.InfoContext(ctx, "skipped unsupported session file", "path", path)
				return nil
			}
			return recordErr
		}
		result.Imported++
		result.Sources = append(result.Sources, source)
		im.logImported(ctx, path, source)
		return nil
	})
	if err != nil {
		return ImportResult{}, fmt.Errorf("walk sessions path: %w", err)
	}
	return result, nil
}

// Read parses and records one supported session file.
func (im *Importer) Read(ctx context.Context, path string, now time.Time) (Source, error) {
	//nolint:gosec // Import reads a user-selected session file path.
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read session file: %w", err)
	}

	source, events, err := parseSessionSource(path, raw, now)
	if err != nil {
		return Source{}, err
	}

	if err := im.store.RecordSource(ctx, source, events); err != nil {
		return Source{}, err
	}
	return source, nil
}

func (im *Importer) logImported(ctx context.Context, path string, source Source) {
	im.logger.InfoContext(ctx, "imported session source",
		"path", path,
		"kind", source.Kind,
		"source_id", source.ID,
	)
}

func parseSessionSource(path string, raw []byte, now time.Time) (Source, []SourceEvent, error) {
	for _, format := range sessionFormats {
		if !format.Sniff(raw) {
			continue
		}
		return assembleSource(format, path, raw, now)
	}
	return Source{}, nil, fmt.Errorf("%w: %s", errUnsupportedSession, path)
}

// assembleSource wraps a format's ParsedSession in the shared Source envelope:
// it hashes the raw bytes for content identity, derives the Source ID from the
// format Kind, stamps timestamps, and back-fills each event's SourceID and index.
func assembleSource(format SessionFormat, uri string, raw []byte, recordedAt time.Time) (Source, []SourceEvent, error) {
	parsed, err := format.Parse(raw)
	if err != nil {
		return Source{}, nil, err
	}

	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	source := Source{
		ID:            SourceID(string(format.Kind()) + ":" + hash),
		Kind:          format.Kind(),
		URI:           uri,
		ContentSHA256: hash,
		Scope:         parsed.Scope,
		StartedAt:     parsed.StartedAt,
		EndedAt:       lastEventTime(parsed.Events),
		RecordedAt:    recordedAt,
		MetadataJSON:  parsed.Metadata,
	}

	for i := range parsed.Events {
		parsed.Events[i].SourceID = source.ID
		parsed.Events[i].Index = i
	}
	return source, parsed.Events, nil
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

// parseTimestamp parses an RFC3339Nano session timestamp, returning the zero
// time for empty or malformed values. Both session formats stamp events this
// way, so the helper is format-neutral.
func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
