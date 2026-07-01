package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/junghwan16/my/internal/jsonutil"
	"github.com/junghwan16/my/internal/source"
)

// The store row-model mappings live in rows.go; the schema lives in the
// migrate package's ledger.

// defaultSearchLimit caps recall results when the caller passes a non-positive
// limit, so a search never dumps the whole store.
const defaultSearchLimit = 20

// defaultMinSimilarity is the cosine-similarity floor for semantic recall.
// Cosine over the embedder's normalized vectors lies in [-1, 1]; an unrelated
// query typically scores near 0 while a topical match scores well above it, so a
// mid-range floor of 0.5 lets paraphrases through while dropping out-of-domain
// noise. Without this floor SearchSemantic always fills to limit, so a query
// like "요리 레시피" against a dev-memory store still returns results (issue #9);
// with it, such below-floor candidates are excluded and the result is empty,
// matching lexical recall. It is a default, overridable per store via
// WithMinSimilarity, not a hardcoded inline literal.
const defaultMinSimilarity = 0.5

// Store records memories and their links back to sources, and maintains the
// full-text index used for recall.
type Store struct {
	db            *bun.DB
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
func NewStore(db *bun.DB, tokenizer Tokenizer) *Store {
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
// leaves lexical recall untouched, so an unreachable Ollama degrades gracefully
// rather than failing. It returns the store for fluent wiring.
func (s *Store) WithEmbedder(embedder Embedder) *Store {
	s.embedder = embedder
	return s
}

// ReplaceSourceMemories atomically replaces every memory produced by one agent for a
// source. Memories linked to the source by the same agent that are absent from the
// new set are deleted, so re-ingesting a source never accumulates stale memories.
func (s *Store) ReplaceSourceMemories(ctx context.Context, sourceID source.SourceID, agent string, memories []Memory, links []Link) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := s.deleteStaleMemories(ctx, tx, sourceID, agent); err != nil {
			return err
		}
		if err := s.upsertMemories(ctx, tx, memories); err != nil {
			return err
		}
		return s.upsertLinks(ctx, tx, links)
	})
}

// deleteStaleMemories removes every memory (and its links and search rows) that
// the agent previously produced for the source, so a re-ingest never accumulates
// stale rows.
func (s *Store) deleteStaleMemories(ctx context.Context, tx bun.Tx, sourceID source.SourceID, agent string) error {
	var staleIDs []string
	if err := tx.NewSelect().
		Model((*memoryRow)(nil)).
		Column("memory.id").
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", sourceID).
		Where("memory.agent = ?", agent).
		Scan(ctx, &staleIDs); err != nil {
		return fmt.Errorf("load stale memories: %w", err)
	}
	if len(staleIDs) == 0 {
		return nil
	}
	if _, err := tx.NewDelete().
		Model((*linkRow)(nil)).
		Where("memory_id IN (?)", bun.List(staleIDs)).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete stale links: %w", err)
	}
	if _, err := tx.NewDelete().
		Model((*memoryRow)(nil)).
		Where("id IN (?)", bun.List(staleIDs)).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete stale memories: %w", err)
	}
	return s.deleteFTS(ctx, tx, staleIDs)
}

func (s *Store) upsertMemories(ctx context.Context, tx bun.Tx, memories []Memory) error {
	for i := range memories {
		mem := memories[i]
		if len(mem.MetadataJSON) == 0 {
			mem.MetadataJSON = jsonutil.EmptyObject()
		}
		if _, err := tx.NewInsert().
			Model(newMemoryRow(mem)).
			On("CONFLICT (id) DO UPDATE").
			Set("agent = EXCLUDED.agent").
			Set("kind = EXCLUDED.kind").
			Set("text = EXCLUDED.text").
			Set("created_at = EXCLUDED.created_at").
			Set("metadata_json = EXCLUDED.metadata_json").
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert memory: %w", err)
		}
		if err := s.indexFTS(ctx, tx, mem); err != nil {
			return err
		}
		if err := s.indexVector(ctx, tx, mem); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertLinks(ctx context.Context, tx bun.Tx, links []Link) error {
	for i := range links {
		link := links[i]
		if link.MemoryID == "" {
			continue
		}
		if len(link.MetadataJSON) == 0 {
			link.MetadataJSON = jsonutil.EmptyObject()
		}
		if _, err := tx.NewInsert().
			Model(newLinkRow(link)).
			On("CONFLICT (source_id, memory_id, kind) DO UPDATE").
			Set("created_at = EXCLUDED.created_at").
			Set("metadata_json = EXCLUDED.metadata_json").
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert memory link: %w", err)
		}
	}
	return nil
}

// SourceHasAgentMemories reports whether a source already has memories from an agent.
func (s *Store) SourceHasAgentMemories(ctx context.Context, sourceID source.SourceID, agent string) (bool, error) {
	count, err := s.db.NewSelect().
		Model((*memoryRow)(nil)).
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", sourceID).
		Where("memory.agent = ?", agent).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("count source agent memories: %w", err)
	}
	return count > 0, nil
}

// SourceMemories lists memories linked to a source.
func (s *Store) SourceMemories(ctx context.Context, id source.SourceID) ([]Memory, error) {
	var rows []memoryRow
	if err := s.db.NewSelect().
		Model(&rows).
		Join("JOIN memory_links AS link ON link.memory_id = memory.id").
		Where("link.source_id = ?", id).
		Order("memory.created_at", "memory.id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load source memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return memories, nil
}

// SourceLinks lists memory links for a source.
func (s *Store) SourceLinks(ctx context.Context, id source.SourceID) ([]Link, error) {
	var rows []linkRow
	if err := s.db.NewSelect().
		Model(&rows).
		Where("source_id = ?", id).
		Order("created_at", "memory_id", "kind").
		Scan(ctx); err != nil {
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

	sql := `SELECT m.id AS id, m.agent AS agent, m.kind AS kind, m.text AS text, m.created_at AS created_at, m.metadata_json AS metadata_json
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

	var rows []memoryRow
	if err := s.db.NewRaw(sql, args...).Scan(ctx, &rows); err != nil {
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
	memoryRow `bun:",extend"`

	VectorBlob []byte `bun:"vector_blob"`
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
// embeds empty, so callers fall back to lexical recall gracefully. Scope and
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
// mirrors SearchMemories (memory_links → sources.scope_value).
func (s *Store) loadCandidateVectors(ctx context.Context, scope string) ([]candidateVector, error) {
	query := s.db.NewSelect().
		Model((*candidateVector)(nil)).
		ColumnExpr("memory.*").
		ColumnExpr("vec.vector AS vector_blob").
		Join("JOIN memory_vectors AS vec ON vec.memory_id = memory.id").
		Where("vec.model = ?", s.embedder.Model())
	if scope != "" {
		query = query.Where(
			"EXISTS (SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id"+
				" WHERE l.memory_id = memory.id AND sr.scope_value = ?)", scope)
	}

	var candidates []candidateVector
	if err := query.Scan(ctx, &candidates); err != nil {
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

// SearchRecollections returns memories matching query, ranked by FTS5 BM25 and
// restricted to scope, each carrying the Source context it was linked from. It
// is the recall read surface the CLI and a future MCP tool share.
func (s *Store) SearchRecollections(ctx context.Context, query, scope string, limit int) ([]Recollection, error) {
	memories, err := s.SearchMemories(ctx, query, scope, limit)
	if err != nil {
		return nil, err
	}
	return s.attachSources(ctx, memories, scope)
}

// RecentRecollections returns the most recently created memories within scope,
// each carrying its Source context. An empty scope spans every scope. It backs
// recall when no task text is given, so the command doubles as a workspace
// memory overview. A non-positive limit falls back to defaultSearchLimit.
func (s *Store) RecentRecollections(ctx context.Context, scope string, limit int) ([]Recollection, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	query := s.db.NewSelect().
		Model((*memoryRow)(nil))
	if scope != "" {
		// A memory can link to several in-scope sources; EXISTS keeps one row
		// per memory (SQLite has no DISTINCT ON) while still filtering by scope.
		query = query.Where(
			"EXISTS (SELECT 1 FROM memory_links AS l JOIN sources AS sr ON sr.id = l.source_id"+
				" WHERE l.memory_id = memory.id AND sr.scope_value = ?)", scope)
	}

	var rows []memoryRow
	if err := query.
		Order("memory.created_at DESC", "memory.id").
		Limit(limit).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load recent memories: %w", err)
	}

	memories := make([]Memory, 0, len(rows))
	for _, row := range rows {
		memories = append(memories, row.toMemory())
	}
	return s.attachSources(ctx, memories, scope)
}

// attachSources loads the Source context for each memory and returns
// recollections in the same order as memories. When scope is non-empty, only
// Sources in that scope are attached, so the recall never leaks a Memory's
// out-of-scope provenance. Sources are ordered by Source ID for stable output.
func (s *Store) attachSources(ctx context.Context, memories []Memory, scope string) ([]Recollection, error) {
	if len(memories) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(memories))
	for _, mem := range memories {
		ids = append(ids, string(mem.ID))
	}

	query := s.db.NewSelect().
		Model((*linkRow)(nil)).
		Column("link.memory_id").
		ColumnExpr("sr.id AS source_id").
		ColumnExpr("sr.uri AS source_uri").
		ColumnExpr("sr.scope_kind AS scope_kind").
		ColumnExpr("sr.scope_value AS scope_value").
		Join("JOIN sources AS sr ON sr.id = link.source_id").
		Where("link.memory_id IN (?)", bun.List(ids))
	if scope != "" {
		query = query.Where("sr.scope_value = ?", scope)
	}

	var refs []sourceRefRow
	if err := query.
		Order("link.memory_id", "sr.id").
		Scan(ctx, &refs); err != nil {
		return nil, fmt.Errorf("load recollection sources: %w", err)
	}
	return assembleRecollections(memories, refs), nil
}

// ReindexMemories rebuilds the full-text index from every stored memory. It is
// idempotent and backfills memories recorded before the index existed.
func (s *Store) ReindexMemories(ctx context.Context) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts"); err != nil {
			return fmt.Errorf("clear fts index: %w", err)
		}
		var rows []memoryRow
		if err := tx.NewSelect().Model(&rows).Scan(ctx); err != nil {
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
// the write path keeps it in sync and this becomes a cheap no-op.
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
func (s *Store) indexFTS(ctx context.Context, tx bun.Tx, mem Memory) error {
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

// indexVector replaces the embedding for one memory inside an open transaction.
// It is a no-op when no embedder is attached, so the default (offline) build
// never touches the vector table. When the embedder is present but the embed
// call fails (e.g. Ollama went away mid-write), the stale vector is cleared and
// the memory is left without one rather than failing the whole write; the
// backfill re-embeds it on a later run once the embedder is healthy again.
func (s *Store) indexVector(ctx context.Context, tx bun.Tx, mem Memory) error {
	if s.embedder == nil {
		return nil
	}
	if _, err := tx.NewDelete().
		Model((*vectorRow)(nil)).
		Where("memory_id = ?", string(mem.ID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("clear memory vector: %w", err)
	}
	vec, ok := embedTolerant(ctx, s.embedder, mem.Text)
	if !ok {
		return nil
	}
	if _, err := tx.NewInsert().
		Model(newVectorRow(string(mem.ID), s.embedder.Model(), vec)).
		Exec(ctx); err != nil {
		return fmt.Errorf("store memory vector: %w", err)
	}
	return nil
}

// embedTolerant embeds text and reports whether a usable vector came back. An
// embed error or empty vector yields ok=false, so callers skip the vector
// (keeping semantic recall optional) instead of failing the surrounding write.
// The error is deliberately swallowed here: fallback is the contract.
func embedTolerant(ctx context.Context, embedder Embedder, text string) (vec []float32, ok bool) {
	vec, err := embedder.Embed(ctx, text)
	if err != nil || len(vec) == 0 {
		return nil, false
	}
	return vec, true
}

// EnsureVectorsIndexed backfills embeddings for memories that lack a current
// vector (never embedded, or embedded by a different model). It is idempotent
// and a no-op when no embedder is attached, mirroring EnsureFTSIndexed. Embed
// failures for individual memories are skipped, so a transient Ollama outage
// leaves the rest indexed and lexical recall unaffected.
func (s *Store) EnsureVectorsIndexed(ctx context.Context) error {
	if s.embedder == nil {
		return nil
	}
	model := s.embedder.Model()
	var rows []memoryRow
	if err := s.db.NewSelect().
		Model(&rows).
		Where("id NOT IN (SELECT memory_id FROM memory_vectors WHERE model = ?)", model).
		Scan(ctx); err != nil {
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
	if _, err := s.db.NewInsert().
		Model(newVectorRow(string(mem.ID), model, vec)).
		On("CONFLICT (memory_id) DO UPDATE").
		Set("model = EXCLUDED.model").
		Set("dim = EXCLUDED.dim").
		Set("vector = EXCLUDED.vector").
		Exec(ctx); err != nil {
		return fmt.Errorf("backfill memory vector: %w", err)
	}
	return nil
}

// deleteFTS removes search rows for memories being replaced, inside a transaction.
func (s *Store) deleteFTS(ctx context.Context, tx bun.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memories_fts WHERE memory_id IN (?)", bun.List(ids)); err != nil {
		return fmt.Errorf("delete stale fts rows: %w", err)
	}
	return nil
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
