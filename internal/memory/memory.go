// Package memories turns imported sources into reusable memory. It
// depends on the source package (ingest reads sources) but never the reverse.
package memories

import (
	"encoding/json"
	"time"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// MemoryID uniquely identifies a generated memory.
type MemoryID string

// MemoryKind describes the kind of memory an agent produced.
type MemoryKind string

// MemoryKindSummary stores an agent-generated source summary.
const MemoryKindSummary MemoryKind = "summary"

// LinkKind describes why a source and a memory are related.
type LinkKind string

// LinkKindSourceIngest links a source to a memory created while ingesting it.
const LinkKindSourceIngest LinkKind = "source_ingest"

// RelationKind describes how two memories relate. Today only one kind exists;
// the column is retained (and part of the relation's key) so future typed
// relations can be added without a schema change.
type RelationKind string

// RelationKindRelates is the single, untyped Memory->Memory relation an agent
// authors during ingest when it connects a new memory to an existing one.
const RelationKindRelates RelationKind = "relates"

// Memory is an agent-produced memory record. Every Memory derives from at least
// one source, stored as a Link.
type Memory struct {
	ID           MemoryID        `json:"id"`
	Agent        string          `json:"agent"`
	Kind         MemoryKind      `json:"kind"`
	Text         string          `json:"text"`
	CreatedAt    time.Time       `json:"created_at"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}

// Link connects a source to an agent-produced memory.
type Link struct {
	SourceID     sourcespkg.SourceID `json:"source_id"`
	MemoryID     MemoryID            `json:"memory_id"`
	Kind         LinkKind            `json:"kind"`
	CreatedAt    time.Time           `json:"created_at"`
	MetadataJSON json.RawMessage     `json:"metadata_json"`
}

// Relation connects one memory to another (Memory<->Memory), distinct from a
// Link (Source->Memory provenance). An agent authors a Relation during ingest to
// point a new memory (From) at an existing one (To) it builds on. The direction
// is always new -> existing.
type Relation struct {
	FromMemoryID MemoryID        `json:"from_memory_id"`
	ToMemoryID   MemoryID        `json:"to_memory_id"`
	Kind         RelationKind    `json:"kind"`
	CreatedAt    time.Time       `json:"created_at"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}
