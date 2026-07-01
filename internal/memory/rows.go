package memory

import (
	"time"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/source"
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
