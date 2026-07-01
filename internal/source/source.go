package source

import (
	"encoding/json"
	"time"
)

// SourceID uniquely identifies an imported source.
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

// Source stores file-level metadata for an imported session. It is immutable
// once recorded; corrections arrive as later Sources or as Memory.
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
