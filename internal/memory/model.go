package memory

import (
	"encoding/json"
	"time"
)

// SourceID uniquely identifies an imported memory source.
type SourceID string

// SourceKind describes the original session file format.
type SourceKind string

const (
	// SourceKindClaudeCodeSession stores Claude Code JSONL sessions.
	SourceKindClaudeCodeSession SourceKind = "claude_code_session"
	// SourceKindCodexSession stores Codex JSONL sessions.
	SourceKindCodexSession SourceKind = "codex_session"
)

// ScopeKind describes the boundary where a source applies.
type ScopeKind string

// ScopeKindWorkspace means events came from a workspace-local session.
const ScopeKindWorkspace ScopeKind = "workspace"

// Scope identifies the workspace or future boundary where memory applies.
type Scope struct {
	Kind  ScopeKind `json:"kind"`
	Value string    `json:"value"`
}

// Source stores file-level metadata for an imported session.
type Source struct {
	ID            SourceID        `json:"id"`
	Kind          SourceKind      `json:"kind"`
	URI           string          `json:"uri"`
	ContentSHA256 string          `json:"content_sha256"`
	Scope         Scope           `json:"scope"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	EndedAt       time.Time       `json:"ended_at,omitempty"`
	RecordedAt    time.Time       `json:"recorded_at"`
	MetadataJSON  json.RawMessage `json:"metadata_json"`
}

// SourceEvent stores one normalized JSONL event from a source.
type SourceEvent struct {
	SourceID    SourceID        `json:"source_id"`
	Index       int             `json:"index"`
	Line        int             `json:"line"`
	At          time.Time       `json:"at,omitempty"`
	Type        string          `json:"type"`
	TurnID      string          `json:"turn_id,omitempty"`
	Role        string          `json:"role,omitempty"`
	Text        string          `json:"text,omitempty"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	RawJSON     json.RawMessage `json:"raw_json"`
}

// ItemID uniquely identifies a generated memory item.
type ItemID string

// ItemKind describes the kind of memory an agent produced.
type ItemKind string

const (
	// ItemKindSummary stores an agent-generated source summary.
	ItemKindSummary ItemKind = "summary"
)

// LinkKind describes why two memory records are related.
type LinkKind string

const (
	// LinkKindSourceIngest links a source to an item created while ingesting it.
	LinkKindSourceIngest LinkKind = "source_ingest"
)

// Item is an agent-produced memory record.
type Item struct {
	ID           ItemID          `json:"id"`
	Agent        string          `json:"agent"`
	Kind         ItemKind        `json:"kind"`
	Text         string          `json:"text"`
	CreatedAt    time.Time       `json:"created_at"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}

// Link connects a source to an agent-produced memory item.
type Link struct {
	SourceID     SourceID        `json:"source_id"`
	ItemID       ItemID          `json:"item_id"`
	Kind         LinkKind        `json:"kind"`
	CreatedAt    time.Time       `json:"created_at"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}

func jsonObject() json.RawMessage {
	return json.RawMessage(`{}`)
}
