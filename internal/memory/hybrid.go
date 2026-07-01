package memory

import (
	"context"
	"sort"
)

// rrfK is the Reciprocal Rank Fusion damping constant. The standard value from
// Cormack et al. (2009); it flattens the contribution of any single ranker so a
// top hit in one list cannot dominate, letting the two engines' complementary
// results reinforce each other. Larger k weights deep ranks more evenly.
const rrfK = 60

// hybridOverfetch is how many results each ranker contributes to the fusion
// pool before the fused list is truncated to the caller's limit. Fetching a
// wider slice per engine gives RRF more overlap to work with, so a memory that
// sits mid-list in both rankers can still surface above one that is #1 in only
// one — the whole point of fusion.
const hybridOverfetch = 50

// HybridRecollections recalls memory by fusing the lexical (FTS5/BM25) and
// semantic (embedding cosine) rankings with Reciprocal Rank Fusion, then
// attaches each memory's Source context. It calls SearchMemories and
// SearchSemantic unchanged and merges their outputs by memory ID, so callers
// get one hybrid-ranked list behind the same Recollection contract. When no
// embedder is attached the semantic list is empty and the result degrades to
// pure lexical order. Scope and limit follow SearchMemories.
func (s *Store) HybridRecollections(ctx context.Context, query, scope string, limit int) ([]Recollection, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	pool := hybridOverfetch
	if limit > pool {
		pool = limit
	}

	lexical, err := s.SearchMemories(ctx, query, scope, pool)
	if err != nil {
		return nil, err
	}
	semantic, err := s.SearchSemantic(ctx, query, scope, pool)
	if err != nil {
		return nil, err
	}

	fused := fuseRRF([][]Memory{lexical, semantic}, limit)
	return s.attachSources(ctx, fused, scope)
}

// fuseRRF merges several ranked memory lists into one hybrid ranking via
// Reciprocal Rank Fusion: each memory scores Σ 1/(rrfK + rank_i) over the lists
// that contain it (rank is 1-based per list), so a memory strong in either
// ranker rises even when weak in the other. Ties break deterministically by
// newest CreatedAt then MemoryID, matching the semantic ranker's tie-break. The
// result is truncated to limit. A memory absent from every list never appears.
func fuseRRF(lists [][]Memory, limit int) []Memory {
	scores := make(map[MemoryID]float64)
	byID := make(map[MemoryID]Memory)
	for _, list := range lists {
		for rank, mem := range list {
			scores[mem.ID] += 1.0 / float64(rrfK+rank+1)
			if _, seen := byID[mem.ID]; !seen {
				byID[mem.ID] = mem
			}
		}
	}

	fused := make([]Memory, 0, len(byID))
	for id := range byID {
		fused = append(fused, byID[id])
	}
	sort.SliceStable(fused, func(i, j int) bool {
		si, sj := scores[fused[i].ID], scores[fused[j].ID]
		if si != sj {
			return si > sj
		}
		if !fused[i].CreatedAt.Equal(fused[j].CreatedAt) {
			return fused[i].CreatedAt.After(fused[j].CreatedAt)
		}
		return fused[i].ID < fused[j].ID
	})

	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused
}
