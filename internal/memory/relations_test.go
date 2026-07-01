package memories_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
)

// relationTestScope is the single workspace every relation test seeds and
// ingests under, so a seeded memory is recalled as related for a later source.
const relationTestScope = "/work/project"

// relatingAgent authors a new memory that points its relates_to at a chosen set
// of ids. It can echo the ids of the related memory it was given (the allowed
// case) and/or inject extra ids the ingest never showed it (the drop case), so a
// test controls exactly which relations reach the store.
type relatingAgent struct {
	name string
	text string
	// echoRelated, when set, adds every related memory's own id to relates_to.
	echoRelated bool
	// extraIDs are appended to relates_to verbatim, standing in for ids the agent
	// invented and that the allowlist must drop.
	extraIDs []memoriespkg.MemoryID
}

func (a relatingAgent) Name() string {
	return a.name
}

func (a relatingAgent) Ingest(_ context.Context, input memoriespkg.AgentInput) (memoriespkg.AgentOutput, error) {
	var relatesTo []memoriespkg.MemoryID
	if a.echoRelated {
		for _, related := range input.RelatedMemories {
			relatesTo = append(relatesTo, related.MemoryID)
		}
	}
	relatesTo = append(relatesTo, a.extraIDs...)
	return memoriespkg.AgentOutput{
		Memories: []memoriespkg.AgentMemory{{
			Kind:      memoriespkg.MemoryKindSummary,
			Text:      a.text,
			RelatesTo: relatesTo,
		}},
	}, nil
}

// seedRelatedMemory ingests one memory under a scope so a later source in the
// same scope recalls it as related context, and returns its id.
func seedRelatedMemory(ctx context.Context, t *testing.T, sources *sourcespkg.Store, memories *memoriespkg.Store, keyword, text string, now time.Time) memoriespkg.MemoryID {
	t.Helper()
	seedSrc := scopedSource(sourcespkg.SourceID("codex_session:seed-"+keyword), relationTestScope)
	if err := sources.SaveSource(ctx, seedSrc, []sourcespkg.SourceEvent{{
		SourceID:    seedSrc.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        keyword,
		PayloadJSON: json.RawMessage(`{}`),
		RawJSON:     json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{staticAgent{name: "seed", text: text}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{seedSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	seeded, err := memories.SourceMemories(ctx, seedSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 1 {
		t.Fatalf("seeded memories = %d, want 1", len(seeded))
	}
	return seeded[0].ID
}

// newSourceInScope saves a fresh source that shares the recall keyword, so ingest
// recalls the seed memory as related context for it.
func newSourceInScope(ctx context.Context, t *testing.T, sources *sourcespkg.Store, keyword string) sourcespkg.Source {
	t.Helper()
	src := scopedSource(sourcespkg.SourceID("codex_session:new-"+keyword), relationTestScope)
	if err := sources.SaveSource(ctx, src, []sourcespkg.SourceEvent{{
		SourceID:    src.ID,
		Index:       0,
		Line:        1,
		Type:        "response_item",
		Role:        "user",
		Text:        keyword,
		PayloadJSON: json.RawMessage(`{}`),
		RawJSON:     json.RawMessage(`{}`),
	}}); err != nil {
		t.Fatal(err)
	}
	return src
}

// mustRelations reads the relations starting from a memory or fails the test, so
// call sites can assert on the count without repeatedly shadowing err.
func mustRelations(ctx context.Context, t *testing.T, memories *memoriespkg.Store, from memoriespkg.MemoryID) []memoriespkg.Relation {
	t.Helper()
	relations, err := memories.MemoryRelations(ctx, from)
	if err != nil {
		t.Fatal(err)
	}
	return relations
}

// TestIngestStoresRelationFromRelatesTo is the #16 happy path: an agent that names
// a shown related memory's id in relates_to yields a stored Memory->Memory
// relation running new -> existing with kind "relates".
func TestIngestStoresRelationFromRelatesTo(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "memory.db"))
	defer closeStores()

	const keyword = "zzrelatekw"
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)

	seedID := seedRelatedMemory(ctx, t, sources, memories, keyword, "seed memory about "+keyword, now)
	newSrc := newSourceInScope(ctx, t, sources, keyword)

	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{relatingAgent{name: "rel", text: "new memory continuing " + keyword, echoRelated: true}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	newMems, err := memories.SourceMemories(ctx, newSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(newMems) != 1 {
		t.Fatalf("new source memories = %d, want 1", len(newMems))
	}
	newID := newMems[0].ID

	relations, err := memories.MemoryRelations(ctx, newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 1 {
		t.Fatalf("relations from new memory = %d, want 1", len(relations))
	}
	rel := relations[0]
	if rel.FromMemoryID != newID {
		t.Fatalf("relation from = %q, want new memory %q", rel.FromMemoryID, newID)
	}
	if rel.ToMemoryID != seedID {
		t.Fatalf("relation to = %q, want seed memory %q", rel.ToMemoryID, seedID)
	}
	if rel.Kind != memoriespkg.RelationKindRelates {
		t.Fatalf("relation kind = %q, want %q", rel.Kind, memoriespkg.RelationKindRelates)
	}
}

// TestIngestDropsRelatesToNotInPrompt proves the allowlist: an id the agent was
// never shown (an invented target) produces no relation, while a legitimately
// shown id still does.
func TestIngestDropsRelatesToNotInPrompt(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "memory.db"))
	defer closeStores()

	const keyword = "zzallowkw"
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)

	seedID := seedRelatedMemory(ctx, t, sources, memories, keyword, "seed memory about "+keyword, now)
	newSrc := newSourceInScope(ctx, t, sources, keyword)

	const bogus = memoriespkg.MemoryID("memory:deadbeefnotshown")
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{relatingAgent{
			name:        "rel",
			text:        "new memory continuing " + keyword,
			echoRelated: true,
			extraIDs:    []memoriespkg.MemoryID{bogus},
		}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	newMems, err := memories.SourceMemories(ctx, newSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	newID := newMems[0].ID

	relations, err := memories.MemoryRelations(ctx, newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 1 {
		t.Fatalf("relations = %d, want 1 (bogus id dropped, seed kept)", len(relations))
	}
	if relations[0].ToMemoryID != seedID {
		t.Fatalf("relation to = %q, want seed %q; bogus id must be dropped", relations[0].ToMemoryID, seedID)
	}
}

// TestIngestReplacesRelationsOnReingest proves a re-ingest of the same
// (source, agent) replaces that run's relations atomically instead of
// accumulating them: after the second run points at nothing, no relation remains.
func TestIngestReplacesRelationsOnReingest(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "memory.db"))
	defer closeStores()

	const keyword = "zzreplacekw"
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)

	seedRelatedMemory(ctx, t, sources, memories, keyword, "seed memory about "+keyword, now)
	newSrc := newSourceInScope(ctx, t, sources, keyword)

	// First run: the new memory relates to the seed.
	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{relatingAgent{name: "rel", text: "continuing " + keyword, echoRelated: true}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}
	firstMems, err := memories.SourceMemories(ctx, newSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRelations(ctx, t, memories, firstMems[0].ID); len(got) != 1 {
		t.Fatalf("relations after first run = %d, want 1", len(got))
	}

	// Re-ingest the same source with the same agent, now authoring no relation.
	// The new memory has different text, so its id changes and the first run's
	// memory (and its relation) must be gone.
	if _, err = memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{relatingAgent{name: "rel", text: "rewritten memory for " + keyword, echoRelated: false}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}

	secondMems, err := memories.SourceMemories(ctx, newSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondMems) != 1 {
		t.Fatalf("new source memories after re-ingest = %d, want 1 (no accumulation)", len(secondMems))
	}
	// The prior run's memory is gone, so its relation must be gone too.
	if got := mustRelations(ctx, t, memories, firstMems[0].ID); len(got) != 0 {
		t.Fatalf("relations from stale first-run memory = %d, want 0 (atomic replacement)", len(got))
	}
	// The rewritten memory authored no relation.
	if got := mustRelations(ctx, t, memories, secondMems[0].ID); len(got) != 0 {
		t.Fatalf("relations from rewritten memory = %d, want 0", len(got))
	}
}

// TestDeletingTargetMemoryCascadesRelations proves deleting the target memory
// leaves no dangling relation, via the ON DELETE CASCADE on to_memory_id. There
// is no public delete-memory API, so the delete is issued directly against the
// same database file the stores share, then the relation is read back through
// the store.
func TestDeletingTargetMemoryCascadesRelations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	sources, memories, closeStores := openStores(ctx, t, path)
	defer closeStores()

	const keyword = "zzcascadekw"
	now := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)

	seedID := seedRelatedMemory(ctx, t, sources, memories, keyword, "seed memory about "+keyword, now)
	newSrc := newSourceInScope(ctx, t, sources, keyword)

	if _, err := memoriespkg.NewIngester(
		sources, memories, []memoriespkg.Agent{relatingAgent{name: "rel", text: "continuing " + keyword, echoRelated: true}}, nil,
	).Ingest(ctx, memoriespkg.IngestOptions{SourceIDs: []sourcespkg.SourceID{newSrc.ID}}, now); err != nil {
		t.Fatal(err)
	}
	newMems, err := memories.SourceMemories(ctx, newSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	newID := newMems[0].ID
	if got := mustRelations(ctx, t, memories, newID); len(got) != 1 {
		t.Fatalf("relations before target delete = %d, want 1", len(got))
	}

	// Delete the target memory directly. foreign_keys is enforced on every
	// connection, so ON DELETE CASCADE removes the relation that pointed at it.
	deleteMemory(ctx, t, path, seedID)

	if got := mustRelations(ctx, t, memories, newID); len(got) != 0 {
		t.Fatalf("relations after target memory deleted = %d, want 0 (cascade, no dangling)", len(got))
	}
}

// deleteMemory removes one memory row directly from the database file, exercising
// the ON DELETE CASCADE that a store has no public API for.
func deleteMemory(ctx context.Context, t *testing.T, path string, id memoriespkg.MemoryID) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	if _, err := db.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", string(id)); err != nil {
		t.Fatal(err)
	}
}
