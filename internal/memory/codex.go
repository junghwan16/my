package memory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

/*
Codex session JSONL format

A Codex session file is newline-delimited JSON. Each line is an envelope:

	{
	  "timestamp": "2026-06-30T01:39:24.928Z",
	  "type": "session_meta|turn_context|response_item|event_msg|compacted",
	  "payload": { ... }
	}

The adapter stores every envelope as a SourceEvent. SourceEvent.Type is the
envelope type. SourceEvent.PayloadJSON is the envelope payload, and
SourceEvent.RawJSON is the full envelope line. Stable fields such as cwd,
turn_id, role, and message text are copied into columns for recall.
*/

func codexSource(uri string, raw []byte, recordedAt time.Time) (Source, []SourceEvent, error) {
	if !looksLikeCodexSession(raw) {
		return Source{}, nil, fmt.Errorf("%w: codex session", errUnsupportedSession)
	}

	events, meta, err := parseCodexEvents(bytes.NewReader(raw))
	if err != nil {
		return Source{}, nil, err
	}

	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	source := Source{
		ID:            SourceID("codex_session:" + hash),
		Kind:          SourceKindCodexSession,
		URI:           uri,
		ContentSHA256: hash,
		Scope: Scope{
			Kind:  ScopeKindWorkspace,
			Value: meta.CWD,
		},
		StartedAt:    meta.StartedAt,
		EndedAt:      lastEventTime(events),
		RecordedAt:   recordedAt,
		MetadataJSON: mustMarshalJSON(meta),
	}

	for i := range events {
		events[i].SourceID = source.ID
		events[i].Index = i
	}

	return source, events, nil
}

func looksLikeCodexSession(raw []byte) bool {
	record := firstJSONLRecord(raw)
	if len(record) == 0 {
		return false
	}

	var envelope codexEnvelope
	if err := json.Unmarshal(record, &envelope); err != nil {
		return false
	}
	return envelope.Type != "" && len(envelope.Payload) > 0
}

type codexMeta struct {
	SessionID     string    `json:"session_id,omitempty"`
	CWD           string    `json:"cwd,omitempty"`
	Originator    string    `json:"originator,omitempty"`
	CLIVersion    string    `json:"cli_version,omitempty"`
	ModelProvider string    `json:"model_provider,omitempty"`
	StartedAt     time.Time `json:"-"`
}

func (m *codexMeta) merge(update codexMeta) {
	if update.SessionID != "" {
		m.SessionID = update.SessionID
	}
	if update.CWD != "" {
		m.CWD = update.CWD
	}
	if update.Originator != "" {
		m.Originator = update.Originator
	}
	if update.CLIVersion != "" {
		m.CLIVersion = update.CLIVersion
	}
	if update.ModelProvider != "" {
		m.ModelProvider = update.ModelProvider
	}
	if !update.StartedAt.IsZero() {
		m.StartedAt = update.StartedAt
	}
}

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	ModelProvider string `json:"model_provider"`
}

type codexTurnContextPayload struct {
	TurnID string `json:"turn_id"`
	CWD    string `json:"cwd"`
}

type codexResponseItemPayload struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []codexContentPart `json:"content"`
}

type codexContentPart struct {
	Text string `json:"text"`
}

func parseCodexEvents(r io.Reader) ([]SourceEvent, codexMeta, error) {
	reader := bufio.NewReader(r)
	var events []SourceEvent
	var meta codexMeta
	lineNumber := 0

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			event, update, parseErr := parseCodexLine(lineNumber, line)
			if parseErr != nil {
				return nil, codexMeta{}, parseErr
			}
			meta.merge(update)
			events = append(events, event)
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, codexMeta{}, fmt.Errorf("read codex session: %w", err)
	}

	return events, meta, nil
}

func parseCodexLine(lineNumber int, line []byte) (SourceEvent, codexMeta, error) {
	raw := bytes.TrimSpace(line)
	if len(raw) == 0 {
		return SourceEvent{}, codexMeta{}, nil
	}

	var envelope codexEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex session line %d: %w", lineNumber, err)
	}
	if envelope.Type == "" {
		return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex session line %d: missing type", lineNumber)
	}
	if len(envelope.Payload) == 0 {
		return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex session line %d: missing payload", lineNumber)
	}

	event := SourceEvent{
		Line:        lineNumber,
		At:          parseCodexTime(envelope.Timestamp),
		Type:        envelope.Type,
		PayloadJSON: cloneJSON(envelope.Payload),
		RawJSON:     cloneJSON(raw),
	}

	var meta codexMeta
	switch envelope.Type {
	case "session_meta":
		var payload codexSessionMetaPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex session_meta line %d: %w", lineNumber, err)
		}
		meta = codexMeta{
			SessionID:     firstNonEmpty(payload.SessionID, payload.ID),
			CWD:           payload.CWD,
			Originator:    payload.Originator,
			CLIVersion:    payload.CLIVersion,
			ModelProvider: payload.ModelProvider,
			StartedAt:     parseCodexTime(firstNonEmpty(payload.Timestamp, envelope.Timestamp)),
		}
	case "turn_context":
		var payload codexTurnContextPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex turn_context line %d: %w", lineNumber, err)
		}
		event.TurnID = payload.TurnID
		if payload.CWD != "" {
			meta.CWD = payload.CWD
		}
	case "response_item":
		var payload codexResponseItemPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return SourceEvent{}, codexMeta{}, fmt.Errorf("parse codex response_item line %d: %w", lineNumber, err)
		}
		event.Role = payload.Role
		event.Text = codexContentText(payload.Content)
	}

	return event, meta, nil
}

func codexContentText(content []codexContentPart) string {
	var text string
	for _, part := range content {
		if part.Text == "" {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += part.Text
	}
	return text
}

func parseCodexTime(value string) time.Time {
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

// mustMarshalJSON marshals values that are statically known to be encodable
// (plain structs and string maps). A failure indicates a programming error, so
// it panics rather than silently dropping data, mirroring regexp.MustCompile.
func mustMarshalJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal json: %v", err))
	}
	return b
}

func cloneJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return cloned
}
