package memories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/junghwan16/gieok/internal/jsonutil"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// The store row-model mappings live in rows.go; the schema lives in the
// migrate package's ledger.

// defaultSearchLimit caps recall results when the caller passes a non-positive
// limit, so a search never dumps the whole store.
const defaultSearchLimit = 20

// defaultMinSimilarity is the cosine-similarity floor for semantic recall. It
// drops the weakest candidates so SearchSemantic does not always fill to limit
// (issue #9), while still admitting paraphrases (issue #10).
//
// The value is empirical, not principled: measured against real bge-m3 vectors,
// cosine is compressed — a legitimate paraphrase ("데이터베이스 스키마 버전 관리",
// top 0.464) and an off-topic query ("요리 레시피", top 0.464) can score
// identically, so NO absolute floor cleanly separates relevant from off-topic.
// 0.40 is chosen recall-first: for a memory tool, missing work the user actually
// did (a paraphrase returning nothing) is worse than returning some low-relevance
// memories for an absurd query. Clean separation needs discriminative memories,
// not a better constant — today's memories are whole-session dumps (issue #7),
// which compresses similarity; re-tune this once memories are focused summaries.
// Overridable per store via WithMinSimilarity.
const defaultMinSimilarity = 0.40

// Store saves memories and their links back to sources, and maintains the
// full-text index used for recall.
type Store struct {
	db            *sql.DB
	tokenizer     Tokenizer
	embedder      Embedder
	minSimilarity float64
}

var (
	_ MemoryWriter = (*Store)(nil)
	_ MemoryReader = (*Store)(nil)
)

// NewStore returns a memory store backed by an already-open database. The
// tokenizer indexes and queries memory text; it must be the same one on both
// paths so a term indexed one way is not missed by a query split differently.
// The store starts with no embedder, so semantic recall is disabled until one
// is attached with WithEmbedder.
func NewStore(db *sql.DB, tokenizer Tokenizer) *Store {
	return &Store{db: db, tokenizer: tokenizer, minSimilarity: defaultMinSimilarity}
}

// WithMinSimilarity overrides the cosine-similarity floor applied to semantic
// recall, so callers can tune how strict out-of-domain filtering is without
// changing the SearchSemantic signature. It returns the store for fluent wiring.
func (s *Store) WithMinSimilarity(min float64) *Store {
	s.minSimilarity = min
	return s
}

// WithEmbedder attaches an optional embedder that enables semantic recall: when
// present, memory text is embedded on write and SearchSemantic can rank by
// cosine similarity. Passing nil (the default) keeps semantic features off and
// leaves lexical recall untouched when Ollama is unreachable rather than
// failing. It returns the store for fluent wiring.
func (s *Store) WithEmbedder(embedder Embedder) *Store {
	s.embedder = embedder
	return s
}

// ReplaceSourceMemories atomically replaces every memory produced by one agent for a
// source. Memories linked to the source by the same agent that are absent from the
// new set are deleted, so re-ingesting a source never accumulates old memories.
// Relations authored by this run (Memory->Memory) are replaced along with their
// source memories: deleting a previous memory cascades to the relations that
// started from it (from_memory_id ON DELETE CASCADE), so a re-ingest never
// accumulates stale relations either. The new relations are inserted after the
// new memories exist, so both endpoints satisfy their foreign keys.
func (s *Store) ReplaceSourceMemories(ctx context.Context, sourceID sourcespkg.SourceID, agent string, memories []Memory, links []Link, relations []Relation) error {
	// Embed OUTSIDE the write transaction. The embedder is a slow network call
	// (a local Ollama round-trip); embedding inside the transaction held the
	// SQLite write lock for its whole duration, so a second process touching the
	// store (e.g. the MCP server) hit "database is locked". Precompute vectors
	// here, then the transaction only does fast local writes.
	vectors := s.embedMemories(ctx, memories)
	return s.runTx(ctx, func(tx *sql.Tx) error {
		if err := s.deletePreviousAgentMemories(ctx, tx, sourceID, agent); err != nil {
			return err
		}
		if err := s.upsertMemories(ctx, tx, memories, vectors); err != nil {
			return err
		}
		if err := s.upsertLinks(ctx, tx, links); err != nil {
			return err
		}
		return s.upsertRelations(ctx, tx, relations)
	})
}

func (s *Store) runTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w; rollback memory transaction: %w", err, rollbackErr)
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit memory transaction: %w", err)
	}
	return nil
}

// embedMemories embeds each memory's text with the store's embedder, outside any
// transaction. It returns nil when no embedder is attached; a per-memory embed
// failure is skipped so one unreachable call never blocks the write.
func (s *Store) embedMemories(ctx context.Context, memories []Memory) map[MemoryID][]float32 {
	if s.embedder == nil {
		return nil
	}
	vectors := make(map[MemoryID][]float32, len(memories))
	for i := range memories {
		if vec, ok := embedTolerant(ctx, s.embedder, memories[i].Text); ok {
			vectors[memories[i].ID] = vec
		}
	}
	return vectors
}

// deletePreviousAgentMemories removes every memory (and its links and search rows) that
// the agent previously produced for the source, so a re-ingest never accumulates
// old rows.
func (s *Store) deletePreviousAgentMemories(ctx context.Context, tx *sql.Tx, sourceID sourcespkg.SourceID, agent string) error {
	previousIDs, err := queryStrings(ctx, tx, `SELECT m.id
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		WHERE link.source_id = ? AND m.agent = ?`, sourceID, agent)
	if err != nil {
		return fmt.Errorf("load previous memories: %w", err)
	}
	if len(previousIDs) == 0 {
		return nil
	}
	if err := execIn(ctx, tx, "DELETE FROM memory_links WHERE memory_id IN", previousIDs); err != nil {
		return fmt.Errorf("delete previous links: %w", err)
	}
	if err := execIn(ctx, tx, "DELETE FROM memories WHERE id IN", previousIDs); err != nil {
		return fmt.Errorf("delete previous memories: %w", err)
	}
	return s.deleteFTS(ctx, tx, previousIDs)
}

func (s *Store) upsertMemories(ctx context.Context, tx *sql.Tx, memories []Memory, vectors map[MemoryID][]float32) error {
	for i := range memories {
		mem := memories[i]
		if len(mem.MetadataJSON) == 0 {
			mem.MetadataJSON = jsonutil.EmptyObject()
		}
		row := newMemoryRow(mem)
		if _, err := tx.ExecContext(ctx, `INSERT INTO memories (
			id,
			agent,
			kind,
			text,
			created_at,
			metadata_json
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			agent = EXCLUDED.agent,
			kind = EXCLUDED.kind,
			text = EXCLUDED.text,
			created_at = EXCLUDED.created_at,
			metadata_json = EXCLUDED.metadata_json`,
			row.ID,
			row.Agent,
			row.Kind,
			row.Text,
			row.CreatedAt,
			row.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert memory: %w", err)
		}
		if err := s.indexFTS(ctx, tx, mem); err != nil {
			return err
		}
		if err := s.writeVector(ctx, tx, mem.ID, vectors[mem.ID]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertLinks(ctx context.Context, tx *sql.Tx, links []Link) error {
	for i := range links {
		link := links[i]
		if link.MemoryID == "" {
			continue
		}
		if len(link.MetadataJSON) == 0 {
			link.MetadataJSON = jsonutil.EmptyObject()
		}
		row := newLinkRow(link)
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_links (
			source_id,
			memory_id,
			kind,
			created_at,
			metadata_json
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (source_id, memory_id, kind) DO UPDATE SET
			created_at = EXCLUDED.created_at,
			metadata_json = EXCLUDED.metadata_json`,
			row.SourceID,
			row.MemoryID,
			row.Kind,
			row.CreatedAt,
			row.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert memory link: %w", err)
		}
	}
	return nil
}

// upsertRelations writes the Memory->Memory relations an agent authored for this
// run. An empty From or To is skipped (a relation needs both endpoints); the
// allowlist that produced these already dropped any To that was not shown to the
// agent, so every survivor points at a real, in-prompt memory.
func (s *Store) upsertRelations(ctx context.Context, tx *sql.Tx, relations []Relation) error {
	for i := range relations {
		relation := relations[i]
		if relation.FromMemoryID == "" || relation.ToMemoryID == "" {
			continue
		}
		if relation.Kind == "" {
			relation.Kind = RelationKindRelates
		}
		if len(relation.MetadataJSON) == 0 {
			relation.MetadataJSON = jsonutil.EmptyObject()
		}
		row := newRelationRow(relation)
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_relations (
			from_memory_id,
			to_memory_id,
			kind,
			created_at,
			metadata_json
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (from_memory_id, to_memory_id, kind) DO UPDATE SET
			created_at = EXCLUDED.created_at,
			metadata_json = EXCLUDED.metadata_json`,
			row.FromMemoryID,
			row.ToMemoryID,
			row.Kind,
			row.CreatedAt,
			row.MetadataJSON,
		); err != nil {
			return fmt.Errorf("upsert memory relation: %w", err)
		}
	}
	return nil
}

// MemoryRelations lists the relations starting from a memory (its outgoing
// Memory->Memory links), ordered for stable output.
func (s *Store) MemoryRelations(ctx context.Context, from MemoryID) ([]Relation, error) {
	rows, err := queryRelationRows(ctx, s.db, relationColumnsSQL()+`
		FROM memory_relations AS rel
		WHERE rel.from_memory_id = ?
		ORDER BY rel.created_at, rel.to_memory_id, rel.kind`, string(from))
	if err != nil {
		return nil, fmt.Errorf("load memory relations: %w", err)
	}
	relations := make([]Relation, 0, len(rows))
	for _, row := range rows {
		relations = append(relations, row.toRelation())
	}
	return relations, nil
}

// Stats reports recall index health: how many memories are stored, how many of
// them carry an embedding vector, and how many rows the full-text index holds.
// A healthy store has Vectors and FTSRows close to Memories; a large gap flags
// an index that needs a backfill (EnsureVectorsIndexed / EnsureFTSIndexed).
type Stats struct {
	Memories int
	Vectors  int
	FTSRows  int
}

// Stats counts the memories, embedding vectors, and full-text index rows so the
// MCP status tool can report recall index health.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories").Scan(&stats.Memories); err != nil {
		return Stats{}, fmt.Errorf("count memories: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memory_vectors").Scan(&stats.Vectors); err != nil {
		return Stats{}, fmt.Errorf("count memory vectors: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories_fts").Scan(&stats.FTSRows); err != nil {
		return Stats{}, fmt.Errorf("count fts rows: %w", err)
	}
	return stats, nil
}

// RecallResultByID loads one memory by id and attaches its Source context, reusing
// the same recall-result assembly path as recall so the structure matches. It reports
// found=false (with a zero RecallResult and no error) when no memory has the id,
// so callers can render a clean "not found" result. Scope is unrestricted: a get
// by id fetches the memory wherever it lives, attaching every Source it derives
// from.
func (s *Store) RecallResultByID(ctx context.Context, id MemoryID) (RecallResult, bool, error) {
	row, err := queryMemoryRow(ctx, s.db, memoryColumnsSQL()+`
		FROM memories AS m
		WHERE m.id = ?`, string(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RecallResult{}, false, nil
		}
		return RecallResult{}, false, fmt.Errorf("load memory by id: %w", err)
	}

	recallResults, err := s.attachSources(ctx, []Memory{row.toMemory()}, "")
	if err != nil {
		return RecallResult{}, false, err
	}
	if len(recallResults) == 0 {
		return RecallResult{}, false, nil
	}
	return recallResults[0], true, nil
}

// SourceHasAgentMemories reports whether a source already has memories from an agent.
func (s *Store) SourceHasAgentMemories(ctx context.Context, sourceID sourcespkg.SourceID, agent string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		WHERE link.source_id = ? AND m.agent = ?`, sourceID, agent).Scan(&count); err != nil {
		return false, fmt.Errorf("count source agent memories: %w", err)
	}
	return count > 0, nil
}

// SourceMemories lists memories linked to a source.
func (s *Store) SourceMemories(ctx context.Context, id sourcespkg.SourceID) ([]Memory, error) {
	rows, err := queryMemoryRows(ctx, s.db, memoryColumnsSQL()+`
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		WHERE link.source_id = ?
		ORDER BY m.created_at, m.id`, id)
	if err != nil {
		return nil, fmt.Errorf("load source memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return memories, nil
}

// SourceLinks lists memory links for a source.
func (s *Store) SourceLinks(ctx context.Context, id sourcespkg.SourceID) ([]Link, error) {
	rows, err := queryLinkRows(ctx, s.db, linkColumnsSQL()+`
		FROM memory_links AS link
		WHERE link.source_id = ?
		ORDER BY link.created_at, link.memory_id, link.kind`, id)
	if err != nil {
		return nil, fmt.Errorf("load source links: %w", err)
	}

	links := make([]Link, 0, len(rows))
	for _, row := range rows {
		links = append(links, row.toLink())
	}
	return links, nil
}

// SearchMemories returns memories whose text matches query, ranked by FTS5 BM25.
// The query is tokenized with the store's tokenizer so it splits the same way
// the index did. When scope is non-empty, only memories linked to a source in
// that scope are returned. A non-positive limit falls back to defaultSearchLimit.
func (s *Store) SearchMemories(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	match := ftsMatch(s.tokenizer.Tokenize(query))
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	sql := memoryColumnsSQL() + `
		FROM (SELECT memory_id, rank FROM memories_fts WHERE memories_fts MATCH ?) AS f
		JOIN memories AS m ON m.id = f.memory_id`
	args := []any{match}
	if scope != "" {
		sql += `
		WHERE EXISTS (
			SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id
			WHERE l.memory_id = m.id AND sr.scope_value = ?)`
		args = append(args, scope)
	}
	sql += `
		ORDER BY f.rank, m.created_at, m.id
		LIMIT ?`
	args = append(args, limit)

	rows, err := queryMemoryRows(ctx, s.db, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return memories, nil
}

// candidateVector is a scoped memory row paired with its stored embedding,
// loaded together so semantic ranking needs a single query per search.
type candidateVector struct {
	memoryRow

	VectorBlob []byte
}

// SearchSemantic ranks memories by cosine similarity between the query
// embedding and each stored memory embedding, returning the top-limit closest
// that clear the store's similarity floor (see defaultMinSimilarity). It embeds
// the query with the store's embedder, loads candidate vectors within scope
// (matching the embedder's model, so a model change cannot mix incomparable
// vectors), and ranks by brute-force cosine in Go — enough for the
// tens-of-thousands scale this store targets. Candidates below the floor are
// dropped, so an out-of-domain query returns nothing rather than filling to
// limit. It returns nil (no error) when no embedder is attached or the query
// embeds empty, so callers can use lexical recall instead. Scope and
// limit follow SearchMemories.
func (s *Store) SearchSemantic(ctx context.Context, query, scope string, limit int) ([]Memory, error) {
	if s.embedder == nil {
		return nil, nil
	}
	queryVec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVec) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	candidates, err := s.loadCandidateVectors(ctx, scope)
	if err != nil {
		return nil, err
	}
	return rankBySimilarity(queryVec, candidates, limit, s.minSimilarity), nil
}

// loadCandidateVectors loads every in-scope memory that has a vector for the
// embedder's current model, each with its raw embedding blob. Scope filtering
// mirrors SearchMemories (memory_links -> sources.scope_value).
func (s *Store) loadCandidateVectors(ctx context.Context, scope string) ([]candidateVector, error) {
	query := memoryColumnsSQL() + `,
		vec.vector
		FROM memories AS m
		JOIN memory_vectors AS vec ON vec.memory_id = m.id
		WHERE vec.model = ?`
	args := []any{s.embedder.Model()}
	if scope != "" {
		query += `
		AND EXISTS (
			SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id
			WHERE l.memory_id = m.id AND sr.scope_value = ?)`
		args = append(args, scope)
	}

	candidates, err := queryCandidateVectors(ctx, s.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load candidate vectors: %w", err)
	}
	return candidates, nil
}

// rankBySimilarity scores every candidate against the query vector by cosine
// similarity, drops any scoring below minSimilarity (so an out-of-domain query
// yields nothing instead of the nearest-but-irrelevant memories), sorts the
// survivors best-first (ties broken by newest then id for stable output), and
// returns at most limit memories. Candidates whose stored vector does not match
// the query's dimension score 0 and are dropped by the same floor.
func rankBySimilarity(queryVec []float32, candidates []candidateVector, limit int, minSimilarity float64) []Memory {
	type scored struct {
		mem   Memory
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, cand := range candidates {
		score := cosineSimilarity(queryVec, decodeVector(cand.VectorBlob))
		if score < minSimilarity {
			continue
		}
		ranked = append(ranked, scored{
			mem:   cand.toMemory(),
			score: score,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if !ranked[i].mem.CreatedAt.Equal(ranked[j].mem.CreatedAt) {
			return ranked[i].mem.CreatedAt.After(ranked[j].mem.CreatedAt)
		}
		return ranked[i].mem.ID < ranked[j].mem.ID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	memories := make([]Memory, 0, len(ranked))
	for _, r := range ranked {
		memories = append(memories, r.mem)
	}
	return memories
}

// SearchRecallResults returns memories matching query, ranked by FTS5 BM25 and
// restricted to scope, each carrying the Source context it was linked from. It
// is the lexical recall read path.
func (s *Store) SearchRecallResults(ctx context.Context, query, scope string, limit int) ([]RecallResult, error) {
	memories, err := s.SearchMemories(ctx, query, scope, limit)
	if err != nil {
		return nil, err
	}
	return s.attachSources(ctx, memories, scope)
}

// RecentRecallResults returns the most recently created memories within scope,
// each carrying its Source context. An empty scope spans every scope. It backs
// recall when no task text is given, so the command doubles as a workspace
// memory overview. A non-positive limit falls back to defaultSearchLimit.
func (s *Store) RecentRecallResults(ctx context.Context, scope string, limit int) ([]RecallResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	query := memoryColumnsSQL() + `
		FROM memories AS m`
	args := []any{}
	if scope != "" {
		// A memory can link to several in-scope sources; EXISTS keeps one row
		// per memory (SQLite has no DISTINCT ON) while still filtering by scope.
		query += `
		WHERE EXISTS (
			SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id
			WHERE l.memory_id = m.id AND sr.scope_value = ?)`
		args = append(args, scope)
	}
	query += `
		ORDER BY m.created_at DESC, m.id
		LIMIT ?`
	args = append(args, limit)

	rows, err := queryMemoryRows(ctx, s.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load recent memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return s.attachSources(ctx, memories, scope)
}

// attachSources loads the Source context for each memory and returns
// recall results in the same order as memories. When scope is non-empty, only
// Sources in that scope are attached, so the recall never leaks a Memory's
// out-of-scope provenance. Sources are ordered by Source ID for stable output.
func (s *Store) attachSources(ctx context.Context, memories []Memory, scope string) ([]RecallResult, error) {
	if len(memories) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(memories))
	for _, mem := range memories {
		ids = append(ids, string(mem.ID))
	}

	query := `SELECT
		link.memory_id,
		sr.id AS source_id,
		sr.uri AS source_uri,
		sr.scope_kind,
		sr.scope_value
	FROM memory_links AS link
	JOIN sources AS sr ON sr.id = link.source_id
	WHERE link.memory_id IN (` + placeholders(len(ids)) + `)`
	args := stringsToAny(ids)
	if scope != "" {
		query += `
		AND sr.scope_value = ?`
		args = append(args, scope)
	}
	query += `
	ORDER BY link.memory_id, sr.id`

	refs, err := querySourceRefRows(ctx, s.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load recall result sources: %w", err)
	}
	return assembleRecallResults(memories, refs), nil
}

// ReindexMemories rebuilds the full-text index from every stored memory. It is
// safe to repeat and backfills memories saved before the index existed.
func (s *Store) ReindexMemories(ctx context.Context) error {
	return s.runTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts"); err != nil {
			return fmt.Errorf("clear fts index: %w", err)
		}
		rows, err := queryMemoryRows(ctx, tx, memoryColumnsSQL()+`
			FROM memories AS m`)
		if err != nil {
			return fmt.Errorf("load memories for reindex: %w", err)
		}
		for _, row := range rows {
			if err := s.indexFTS(ctx, tx, row.toMemory()); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureFTSIndexed backfills the index once when it is empty but memories exist,
// so recall works on a store populated before the index was added. Once filled,
// the write path keeps it in sync and this returns quickly.
func (s *Store) EnsureFTSIndexed(ctx context.Context) error {
	var ftsCount int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories_fts").Scan(&ftsCount); err != nil {
		return fmt.Errorf("count fts rows: %w", err)
	}
	if ftsCount > 0 {
		return nil
	}
	var memCount int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM memories").Scan(&memCount); err != nil {
		return fmt.Errorf("count memories: %w", err)
	}
	if memCount == 0 {
		return nil
	}
	return s.ReindexMemories(ctx)
}

// indexFTS replaces the search row for one memory inside an open transaction.
func (s *Store) indexFTS(ctx context.Context, tx *sql.Tx, mem Memory) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts WHERE memory_id = ?", string(mem.ID)); err != nil {
		return fmt.Errorf("clear fts row: %w", err)
	}
	tokens := strings.Join(s.tokenizer.Tokenize(mem.Text), " ")
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO memories_fts (memory_id, tokens) VALUES (?, ?)", string(mem.ID), tokens,
	); err != nil {
		return fmt.Errorf("index memory for search: %w", err)
	}
	return nil
}

// writeVector replaces a memory's stored embedding inside the transaction. The
// vector is precomputed outside the transaction (see embedMemories), so this
// does only fast local writes and never holds the lock during an embed. A nil
// vec (no embedder, or a skipped embed) just clears any old row.
func (s *Store) writeVector(ctx context.Context, tx *sql.Tx, memID MemoryID, vec []float32) error {
	if s.embedder == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_vectors WHERE memory_id = ?", string(memID)); err != nil {
		return fmt.Errorf("clear memory vector: %w", err)
	}
	if len(vec) == 0 {
		return nil
	}
	row := newVectorRow(string(memID), s.embedder.Model(), vec)
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_vectors (
		memory_id,
		model,
		dim,
		vector
	) VALUES (?, ?, ?, ?)`,
		row.MemoryID,
		row.Model,
		row.Dim,
		row.Vector,
	); err != nil {
		return fmt.Errorf("store memory vector: %w", err)
	}
	return nil
}

// embedTolerant embeds text and reports whether a usable vector came back. An
// embed error or empty vector yields ok=false, so callers skip the vector
// (keeping semantic recall optional) instead of failing the surrounding write.
// The error is deliberately swallowed here: skipping the vector is the contract.
func embedTolerant(ctx context.Context, embedder Embedder, text string) (vec []float32, ok bool) {
	vec, err := embedder.Embed(ctx, text)
	if err != nil || len(vec) == 0 {
		return nil, false
	}
	return vec, true
}

// EnsureVectorsIndexed backfills embeddings for memories that lack a current
// vector (never embedded, or embedded by a different model). It is safe to repeat
// and returns quickly when no embedder is attached, mirroring EnsureFTSIndexed. Embed
// failures for individual memories are skipped, so a transient Ollama outage
// leaves the rest indexed and lexical recall unaffected.
func (s *Store) EnsureVectorsIndexed(ctx context.Context) error {
	if s.embedder == nil {
		return nil
	}
	model := s.embedder.Model()
	rows, err := queryMemoryRows(ctx, s.db, memoryColumnsSQL()+`
		FROM memories AS m
		WHERE m.id NOT IN (SELECT memory_id FROM memory_vectors WHERE model = ?)`, model)
	if err != nil {
		return fmt.Errorf("load memories for vector backfill: %w", err)
	}
	for _, row := range rows {
		if err := s.backfillVector(ctx, row.toMemory(), model); err != nil {
			return err
		}
	}
	return nil
}

// backfillVector embeds one memory and upserts its vector in its own
// transaction. An embed failure is skipped (returns nil) so one unreachable
// call does not abort the whole backfill.
func (s *Store) backfillVector(ctx context.Context, mem Memory, model string) error {
	vec, ok := embedTolerant(ctx, s.embedder, mem.Text)
	if !ok {
		return nil
	}
	row := newVectorRow(string(mem.ID), model, vec)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_vectors (
		memory_id,
		model,
		dim,
		vector
	) VALUES (?, ?, ?, ?)
	ON CONFLICT (memory_id) DO UPDATE SET
		model = EXCLUDED.model,
		dim = EXCLUDED.dim,
		vector = EXCLUDED.vector`,
		row.MemoryID,
		row.Model,
		row.Dim,
		row.Vector,
	); err != nil {
		return fmt.Errorf("backfill memory vector: %w", err)
	}
	return nil
}

// deleteFTS removes search rows for memories being replaced, inside a transaction.
func (s *Store) deleteFTS(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := execIn(ctx, tx, "DELETE FROM memories_fts WHERE memory_id IN", ids); err != nil {
		return fmt.Errorf("delete previous fts rows: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type sqlQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func memoryColumnsSQL() string {
	return `SELECT
		m.id,
		m.agent,
		m.kind,
		m.text,
		m.created_at,
		m.metadata_json`
}

func linkColumnsSQL() string {
	return `SELECT
		link.source_id,
		link.memory_id,
		link.kind,
		link.created_at,
		link.metadata_json`
}

func relationColumnsSQL() string {
	return `SELECT
		rel.from_memory_id,
		rel.to_memory_id,
		rel.kind,
		rel.created_at,
		rel.metadata_json`
}

func queryMemoryRow(ctx context.Context, db sqlQueryer, query string, args ...any) (memoryRow, error) {
	return scanMemoryRow(db.QueryRowContext(ctx, query, args...))
}

func queryMemoryRows(ctx context.Context, db sqlQueryer, query string, args ...any) ([]memoryRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	memories := []memoryRow{}
	for rows.Next() {
		row, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return memories, nil
}

func scanMemoryRow(scanner rowScanner) (memoryRow, error) {
	var row memoryRow
	var createdAt any
	if err := scanner.Scan(
		&row.ID,
		&row.Agent,
		&row.Kind,
		&row.Text,
		&createdAt,
		&row.MetadataJSON,
	); err != nil {
		return memoryRow{}, err
	}
	var err error
	if row.CreatedAt, err = scanRequiredTime(createdAt); err != nil {
		return memoryRow{}, fmt.Errorf("parse created_at: %w", err)
	}
	return row, nil
}

func queryLinkRows(ctx context.Context, db sqlQueryer, query string, args ...any) ([]linkRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	links := []linkRow{}
	for rows.Next() {
		row, err := scanLinkRow(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return links, nil
}

func scanLinkRow(scanner rowScanner) (linkRow, error) {
	var row linkRow
	var createdAt any
	if err := scanner.Scan(
		&row.SourceID,
		&row.MemoryID,
		&row.Kind,
		&createdAt,
		&row.MetadataJSON,
	); err != nil {
		return linkRow{}, err
	}
	var err error
	if row.CreatedAt, err = scanRequiredTime(createdAt); err != nil {
		return linkRow{}, fmt.Errorf("parse created_at: %w", err)
	}
	return row, nil
}

func queryRelationRows(ctx context.Context, db sqlQueryer, query string, args ...any) ([]relationRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	relations := []relationRow{}
	for rows.Next() {
		row, err := scanRelationRow(rows)
		if err != nil {
			return nil, err
		}
		relations = append(relations, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return relations, nil
}

func scanRelationRow(scanner rowScanner) (relationRow, error) {
	var row relationRow
	var createdAt any
	if err := scanner.Scan(
		&row.FromMemoryID,
		&row.ToMemoryID,
		&row.Kind,
		&createdAt,
		&row.MetadataJSON,
	); err != nil {
		return relationRow{}, err
	}
	var err error
	if row.CreatedAt, err = scanRequiredTime(createdAt); err != nil {
		return relationRow{}, fmt.Errorf("parse created_at: %w", err)
	}
	return row, nil
}

func queryCandidateVectors(ctx context.Context, db sqlQueryer, query string, args ...any) ([]candidateVector, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	candidates := []candidateVector{}
	for rows.Next() {
		row, err := scanCandidateVector(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func scanCandidateVector(scanner rowScanner) (candidateVector, error) {
	var row candidateVector
	var createdAt any
	if err := scanner.Scan(
		&row.ID,
		&row.Agent,
		&row.Kind,
		&row.Text,
		&createdAt,
		&row.MetadataJSON,
		&row.VectorBlob,
	); err != nil {
		return candidateVector{}, err
	}
	var err error
	if row.CreatedAt, err = scanRequiredTime(createdAt); err != nil {
		return candidateVector{}, fmt.Errorf("parse created_at: %w", err)
	}
	return row, nil
}

func querySourceRefRows(ctx context.Context, db sqlQueryer, query string, args ...any) ([]sourceRefRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	refs := []sourceRefRow{}
	for rows.Next() {
		var row sourceRefRow
		if err := rows.Scan(&row.MemoryID, &row.SourceID, &row.SourceURI, &row.ScopeKind, &row.ScopeValue); err != nil {
			return nil, err
		}
		refs = append(refs, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func queryStrings(ctx context.Context, db sqlQueryer, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func execIn(ctx context.Context, db sqlExecer, prefix string, values []string) error {
	if len(values) == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, prefix+" ("+placeholders(len(values))+")", stringsToAny(values)...)
	return err
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		return
	}
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringsToAny(values []string) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func scanRequiredTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		return parseSQLiteTime(v)
	case []byte:
		return parseSQLiteTime(string(v))
	default:
		return time.Time{}, fmt.Errorf("unsupported time value %T", value)
	}
}

func parseSQLiteTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", value)
}

// ftsMatch builds an FTS5 MATCH expression from tokens: each token is quoted
// (so it is treated as a literal string, safe from FTS operator characters) and
// joined with spaces, which FTS5 reads as an implicit AND.
func ftsMatch(tokens []string) string {
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}
