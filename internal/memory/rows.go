package memory

import (
	"time"

	"github.com/uptrace/bun"

	"github.com/junghwan16/gieok/internal/source"
)

// These row models map onto the memories and memory_links tables. The schema
// that creates and evolves them lives in the migrate package's ledger; keep the
// column tags here in sync with those migrations.

type memoryRow struct {
	bun.BaseModel `bun:"table:memories,alias:memory"`

	ID           string    `bun:"id,pk"`
	Agent        string    `bun:"agent,notnull"`
	Kind         string    `bun:"kind,notnull"`
	Text         string    `bun:"text,notnull"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	MetadataJSON string    `bun:"metadata_json,notnull"`
}

type linkRow struct {
	bun.BaseModel `bun:"table:memory_links,alias:link"`

	SourceID     string    `bun:"source_id,pk"`
	MemoryID     string    `bun:"memory_id,pk"`
	Kind         string    `bun:"kind,pk"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	MetadataJSON string    `bun:"metadata_json,notnull"`
}

func newMemoryRow(memory Memory) *memoryRow {
	return &memoryRow{
		ID:           string(memory.ID),
		Agent:        memory.Agent,
		Kind:         string(memory.Kind),
		Text:         memory.Text,
		CreatedAt:    memory.CreatedAt,
		MetadataJSON: string(memory.MetadataJSON),
	}
}

func (r memoryRow) toMemory() Memory {
	return Memory{
		ID:           MemoryID(r.ID),
		Agent:        r.Agent,
		Kind:         MemoryKind(r.Kind),
		Text:         r.Text,
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

func newLinkRow(link Link) *linkRow {
	return &linkRow{
		SourceID:     string(link.SourceID),
		MemoryID:     string(link.MemoryID),
		Kind:         string(link.Kind),
		CreatedAt:    link.CreatedAt,
		MetadataJSON: string(link.MetadataJSON),
	}
}

func (r linkRow) toLink() Link {
	return Link{
		SourceID:     source.SourceID(r.SourceID),
		MemoryID:     MemoryID(r.MemoryID),
		Kind:         LinkKind(r.Kind),
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

// sourceRefRow is the flat projection of a memory's linked source used to build
// recollections: the memory ID it belongs to plus that source's identity and
// scope. It is scanned from a memory_links-to-sources join, not a single table.
type sourceRefRow struct {
	MemoryID   string `bun:"memory_id"`
	SourceID   string `bun:"source_id"`
	SourceURI  string `bun:"source_uri"`
	ScopeKind  string `bun:"scope_kind"`
	ScopeValue string `bun:"scope_value"`
}

func (r sourceRefRow) toSourceRef() SourceRef {
	return SourceRef{
		ID:  source.SourceID(r.SourceID),
		URI: r.SourceURI,
		Scope: source.Scope{
			Kind:  source.ScopeKind(r.ScopeKind),
			Value: r.ScopeValue,
		},
	}
}

// assembleRecollections joins ordered memories with their source refs, keeping
// the memory order the caller ranked them in. Refs are grouped by memory ID; a
// memory with no in-scope source gets an empty Sources slice rather than being
// dropped.
func assembleRecollections(memories []Memory, refs []sourceRefRow) []Recollection {
	byMemory := make(map[string][]SourceRef, len(memories))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		key := ref.MemoryID + "\x00" + ref.SourceID
		if seen[key] {
			continue
		}
		seen[key] = true
		byMemory[ref.MemoryID] = append(byMemory[ref.MemoryID], ref.toSourceRef())
	}

	recollections := make([]Recollection, 0, len(memories))
	for _, mem := range memories {
		recollections = append(recollections, Recollection{
			MemoryID:  mem.ID,
			Agent:     mem.Agent,
			Kind:      mem.Kind,
			Text:      mem.Text,
			CreatedAt: mem.CreatedAt,
			Sources:   byMemory[string(mem.ID)],
		})
	}
	return recollections
}
