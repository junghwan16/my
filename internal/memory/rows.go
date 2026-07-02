package memories

import (
	"database/sql"
	"time"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// These row models map onto the memories and memory_links tables. The schema
// that creates and evolves them lives in internal/migrate; keep the
// column tags here in sync with those migrations.

type memoryRow struct {
	ID           string
	Agent        string
	Kind         string
	Text         string
	CreatedAt    time.Time
	MetadataJSON string
	// TextOverride is the human correction, NULL when the memory is unedited.
	TextOverride sql.NullString
}

type linkRow struct {
	SourceID     string
	MemoryID     string
	Kind         string
	CreatedAt    time.Time
	MetadataJSON string
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
		Override:     r.TextOverride.String,
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
		SourceID:     sourcespkg.SourceID(r.SourceID),
		MemoryID:     MemoryID(r.MemoryID),
		Kind:         LinkKind(r.Kind),
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

type relationRow struct {
	FromMemoryID string
	ToMemoryID   string
	Kind         string
	CreatedAt    time.Time
	MetadataJSON string
}

func newRelationRow(relation Relation) *relationRow {
	return &relationRow{
		FromMemoryID: string(relation.FromMemoryID),
		ToMemoryID:   string(relation.ToMemoryID),
		Kind:         string(relation.Kind),
		CreatedAt:    relation.CreatedAt,
		MetadataJSON: string(relation.MetadataJSON),
	}
}

func (r relationRow) toRelation() Relation {
	return Relation{
		FromMemoryID: MemoryID(r.FromMemoryID),
		ToMemoryID:   MemoryID(r.ToMemoryID),
		Kind:         RelationKind(r.Kind),
		CreatedAt:    r.CreatedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

// sourceRefRow is the flat projection of a memory's linked source used to build
// recall results: the memory ID it belongs to plus that source's identity and
// scope. It is scanned from a memory_links-to-sources join, not a single table.
type sourceRefRow struct {
	MemoryID   string
	SourceID   string
	SourceURI  string
	ScopeKind  string
	ScopeValue string
}

func (r sourceRefRow) toSourceRef() SourceRef {
	return SourceRef{
		ID:  sourcespkg.SourceID(r.SourceID),
		URI: r.SourceURI,
		Scope: sourcespkg.Scope{
			Kind:  sourcespkg.ScopeKind(r.ScopeKind),
			Value: r.ScopeValue,
		},
	}
}

// assembleRecallResults joins ordered memories with their source refs, keeping
// the memory order the caller ranked them in. Refs are grouped by memory ID; a
// memory with no in-scope source gets an empty Sources slice rather than being
// dropped.
func assembleRecallResults(memories []Memory, refs []sourceRefRow) []RecallResult {
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

	recallResults := make([]RecallResult, 0, len(memories))
	for _, mem := range memories {
		originalText := ""
		if mem.Edited() {
			originalText = mem.Text
		}
		recallResults = append(recallResults, RecallResult{
			MemoryID:     mem.ID,
			Agent:        mem.Agent,
			Kind:         mem.Kind,
			Text:         mem.EffectiveText(),
			Edited:       mem.Edited(),
			OriginalText: originalText,
			CreatedAt:    mem.CreatedAt,
			Sources:      byMemory[string(mem.ID)],
		})
	}
	return recallResults
}
