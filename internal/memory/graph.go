package memories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// defaultGraphNodeCap bounds how many nodes the provenance graph returns when
// the caller passes a non-positive cap. A browser graph becomes unusable past a
// couple thousand nodes (ADR-0008), so the scope overview is truncated to the
// densest Memory neighborhoods and the rest is reached by click-to-expand
// drilldown; the aggregate panel still reports the full, un-capped counts.
const defaultGraphNodeCap = 500

// GraphNodeKind labels a graph node as a Source (원본) or a Memory (기억), so the
// UI can style and size the two node families differently.
type GraphNodeKind string

const (
	// GraphNodeSource is an imported Source node (원본).
	GraphNodeSource GraphNodeKind = "source"
	// GraphNodeMemory is an agent-produced Memory node (기억).
	GraphNodeMemory GraphNodeKind = "memory"
)

// GraphNode is one node in the provenance graph: a Source or a Memory. Size is
// the node's "melted-in-ness" metric, which the UI turns into node diameter, and
// its meaning depends on Kind (ADR-0008): for a Source it is fan-OUT — how many
// in-scope Memories melted out of it; for a Memory it is Relation degree — how
// many other Memories it connects to via Memory<->Memory Relations. Per-Memory
// Link fan-in is deliberately not a size: a Memory id hashes its single Source,
// so its Link fan-in is structurally always 1 and carries no signal. Scope
// carries a Source node's workspace so the UI can label provenance; it is empty
// for Memory nodes, which span whichever Sources they derive from.
type GraphNode struct {
	ID    string           `json:"id"`
	Kind  GraphNodeKind    `json:"kind"`
	Label string           `json:"label"`
	Size  int              `json:"size"`
	Scope sourcespkg.Scope `json:"scope"`
}

// GraphEdge is one Link edge (Source->Memory provenance) in the graph.
type GraphEdge struct {
	SourceID string `json:"source_id"`
	MemoryID string `json:"memory_id"`
}

// GraphRelation is one Relation edge (Memory<->Memory, ADR-0007) in the graph,
// authored by an agent during ingest and stored in memory_relations. It runs
// from the newer Memory to the existing one it built on, but the UI treats it as
// an undirected knowledge tie distinct from a Link (Source->Memory) edge. It is
// kept in its own slice so the JSON contract separates the two edge families and
// the renderer can style them differently; an empty slice degrades the view to
// provenance-only with no regression.
type GraphRelation struct {
	FromMemoryID string `json:"from_memory_id"`
	ToMemoryID   string `json:"to_memory_id"`
}

// GraphStats is the aggregate panel over the whole selected scope. It is
// computed independent of the node cap, so the panel always reports the true
// totals even when the returned nodes/edges are truncated: total in-scope
// Sources, total in-scope Memories, and the average number of Sources melted
// into a Memory (total Links over total Memories, zero when there are no
// Memories).
type GraphStats struct {
	Sources          int     `json:"sources"`
	Memories         int     `json:"memories"`
	AvgSourcesPerMem float64 `json:"avg_sources_per_memory"`
}

// Graph is the provenance graph for one scope: Source and Memory nodes with Link
// (Source->Memory) edges and Relation (Memory<->Memory) edges, plus the aggregate
// panel. Truncated reports whether the node cap dropped part of the scope, so the
// UI can tell the user the overview is partial and to drill down for the rest.
// Nodes, Edges, and Relations are never nil (empty slices for an empty store,
// empty scope, or a scope with no Relations), so the JSON contract is stable and
// a Relation-free scope degrades cleanly to the provenance-only view.
type Graph struct {
	Nodes     []GraphNode     `json:"nodes"`
	Edges     []GraphEdge     `json:"edges"`
	Relations []GraphRelation `json:"relations"`
	Stats     GraphStats      `json:"stats"`
	Truncated bool            `json:"truncated"`
}

// Graph builds the provenance graph for a scope: the Source and Memory nodes, the
// Link edges (Source->Memory) between them, and the Relation edges
// (Memory<->Memory, ADR-0007) among the Memories. Source nodes are sized by
// fan-out (how many Memories melted out of them) and Memory nodes by Relation
// degree (how many other Memories they connect to). An empty scope spans every
// scope. It is a read-model query for the web /graph page and never touches the
// recall path.
//
// Scoping mirrors recall's no-leak rule: a Source belongs to the scope when its
// scope_value matches; a Memory belongs when it links to at least one in-scope
// Source; a Link edge is emitted only when both its endpoints are in scope; and a
// Relation edge is emitted only between two kept in-scope Memory nodes, so a
// Memory's out-of-scope provenance or relations never leak into a scoped view.
//
// cap bounds the returned node count so a browser graph stays renderable; a
// non-positive cap falls back to defaultGraphNodeCap. When the scope holds more
// nodes than the cap, the most-connected Memories (highest Relation degree first)
// and the Sources they derive from are kept, Truncated is set, and the rest is
// reached by MemoryNeighborhood drilldown. The aggregate Stats are always
// computed over the whole scope, independent of the cap.
func (s *Store) Graph(ctx context.Context, scope string, cap int) (Graph, error) {
	if cap <= 0 {
		cap = defaultGraphNodeCap
	}

	stats, err := s.graphStats(ctx, scope)
	if err != nil {
		return Graph{}, err
	}

	degree, err := s.memoryRelationDegree(ctx, scope)
	if err != nil {
		return Graph{}, err
	}

	// Keep the most-connected Memories first (highest Relation degree, then id
	// for stable output), then take Sources they link to, until the cap is
	// reached. This keeps a truncated overview centered on the densest Relation
	// neighborhoods rather than an arbitrary slice.
	graph := Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, Relations: []GraphRelation{}, Stats: stats}

	memNodes, memIDs, memTruncated := selectMemoryNodes(degree, cap)

	sourceNodes, edges, srcTruncated, err := s.graphSourcesAndEdges(ctx, scope, memIDs, cap-len(memNodes))
	if err != nil {
		return Graph{}, err
	}

	// Relation edges only between two kept Memory nodes, so the graph never
	// references a Memory dropped by the cap and an out-of-scope Relation target
	// never leaks in.
	relations, err := s.graphRelations(ctx, memIDs)
	if err != nil {
		return Graph{}, err
	}

	graph.Truncated = memTruncated || srcTruncated
	graph.Nodes = append(graph.Nodes, memNodes...)
	graph.Nodes = append(graph.Nodes, sourceNodes...)
	graph.Edges = edges
	graph.Relations = relations
	return graph, nil
}

// memoryDegreeRow pairs an in-scope Memory with its label text and Relation
// degree — the number of distinct other Memories it connects to via a
// Memory<->Memory Relation, counting both directions. Rows are ordered
// densest-first so the cap keeps the most-connected Memories.
type memoryDegreeRow struct {
	ID     string
	Label  string
	Degree int
}

// memoryRelationDegree loads every in-scope Memory (linked to at least one
// in-scope Source, mirroring recall's no-leak rule) with its Relation degree: the
// count of distinct other Memories it relates to over both directions of
// memory_relations. Memories with no Relation still appear with degree 0, so a
// Relation-free scope degrades cleanly to the provenance-only view. Rows are
// ordered by degree desc then id for stable, densest-first truncation. An empty
// scope spans every scope.
func (s *Store) memoryRelationDegree(ctx context.Context, scope string) ([]memoryDegreeRow, error) {
	// A LEFT JOIN keeps Relation-free Memories (degree 0); counting the distinct
	// neighbour over both from/to directions gives an undirected degree.
	query := `SELECT m.id, m.text, count(DISTINCT rel.other) AS degree
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		JOIN sources AS sr ON sr.id = link.source_id
		LEFT JOIN (
			SELECT from_memory_id AS mem, to_memory_id AS other FROM memory_relations
			UNION
			SELECT to_memory_id AS mem, from_memory_id AS other FROM memory_relations
		) AS rel ON rel.mem = m.id`
	args := []any{}
	if scope != "" {
		query += `
		WHERE sr.scope_value = ?`
		args = append(args, scope)
	}
	query += `
		GROUP BY m.id
		ORDER BY degree DESC, m.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load memory relation degree: %w", err)
	}
	defer closeRows(rows)

	degree := []memoryDegreeRow{}
	for rows.Next() {
		var row memoryDegreeRow
		if err := rows.Scan(&row.ID, &row.Label, &row.Degree); err != nil {
			return nil, fmt.Errorf("scan memory relation degree: %w", err)
		}
		degree = append(degree, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory relation degree: %w", err)
	}
	return degree, nil
}

// selectMemoryNodes turns the densest-first Relation-degree rows into Memory
// nodes up to half the cap, so both Memory and Source nodes fit under the budget.
// It reports which Memory ids were kept (to scope the edge and Relation queries)
// and whether any Memory was dropped. A Memory node's label is its text, sized by
// its Relation degree in the UI.
func selectMemoryNodes(degree []memoryDegreeRow, cap int) ([]GraphNode, []string, bool) {
	// Reserve at most half the budget for Memory nodes so their Sources also
	// fit; a Memory with no room is dropped and reached by drilldown.
	memBudget := cap / 2
	if memBudget < 1 && cap >= 1 {
		memBudget = 1
	}

	limit := len(degree)
	truncated := false
	if limit > memBudget {
		limit = memBudget
		truncated = true
	}

	nodes := make([]GraphNode, 0, limit)
	ids := make([]string, 0, limit)
	for _, row := range degree[:limit] {
		nodes = append(nodes, GraphNode{
			ID:    row.ID,
			Kind:  GraphNodeMemory,
			Label: row.Label,
			Size:  row.Degree,
		})
		ids = append(ids, row.ID)
	}
	return nodes, ids, truncated
}

// graphSourcesAndEdges loads the Source nodes and Link edges for the kept
// Memories: every in-scope Source that melted into one of memIDs, and the
// (Source, Memory) Link between them. Each Source node is sized by its fan-out —
// the total number of distinct Memories that melted out of it (ADR-0008), which
// is a property of the Source and so counts every Memory it links to, not only
// the kept ones. It caps the Source nodes at budget so the whole graph stays
// under the node cap; edges to a dropped Source are omitted so the graph never
// references a missing node, and truncated reports whether any Source was
// dropped. An empty memIDs yields no sources and no edges.
func (s *Store) graphSourcesAndEdges(ctx context.Context, scope string, memIDs []string, budget int) (_ []GraphNode, _ []GraphEdge, truncated bool, _ error) {
	if len(memIDs) == 0 || budget <= 0 {
		return []GraphNode{}, []GraphEdge{}, len(memIDs) > 0, nil
	}

	// fan_out is the Source's total Link fan-out (distinct Memories melted out of
	// it); it is a property of the Source, so it is not narrowed to memIDs.
	query := `SELECT sr.id, sr.scope_kind, sr.scope_value, link.memory_id,
		(SELECT count(DISTINCT fo.memory_id) FROM memory_links AS fo WHERE fo.source_id = sr.id) AS fan_out
		FROM memory_links AS link
		JOIN sources AS sr ON sr.id = link.source_id
		WHERE link.memory_id IN (` + placeholders(len(memIDs)) + `)`
	args := stringsToAny(memIDs)
	if scope != "" {
		query += `
		AND sr.scope_value = ?`
		args = append(args, scope)
	}
	query += `
		ORDER BY sr.id, link.memory_id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, false, fmt.Errorf("load graph sources and edges: %w", err)
	}
	defer closeRows(rows)

	sourceNodes := []GraphNode{}
	edges := []GraphEdge{}
	seenSource := make(map[string]bool)
	for rows.Next() {
		var srcID, scopeKind, scopeValue, memoryID string
		var fanOut int
		if err := rows.Scan(&srcID, &scopeKind, &scopeValue, &memoryID, &fanOut); err != nil {
			return nil, nil, false, fmt.Errorf("scan graph source: %w", err)
		}
		if !seenSource[srcID] {
			if len(seenSource) >= budget {
				// Out of Source budget: drop this Source and every edge to it,
				// so the graph stays under the cap and references no missing node.
				truncated = true
				continue
			}
			seenSource[srcID] = true
			sourceNodes = append(sourceNodes, GraphNode{
				ID:    srcID,
				Kind:  GraphNodeSource,
				Label: srcID,
				Size:  fanOut,
				Scope: sourcespkg.Scope{Kind: sourcespkg.ScopeKind(scopeKind), Value: scopeValue},
			})
		}
		edges = append(edges, GraphEdge{SourceID: srcID, MemoryID: memoryID})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("iterate graph sources: %w", err)
	}
	return sourceNodes, edges, truncated, nil
}

// graphStats computes the aggregate panel over the whole scope, independent of
// the node cap: total in-scope Sources, total in-scope Memories, and the average
// number of Sources per Memory (total in-scope Links over total Memories). The
// average is zero when the scope holds no Memories, so an empty scope reports a
// clean zeroed panel.
func (s *Store) graphStats(ctx context.Context, scope string) (GraphStats, error) {
	var stats GraphStats

	sourceQuery := `SELECT count(*) FROM sources AS sr`
	memoryQuery := `SELECT count(DISTINCT m.id)
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		JOIN sources AS sr ON sr.id = link.source_id`
	// A Link here is one in-scope (Source, Memory) provenance edge; summing them
	// and dividing by Memories gives the average Sources melted into a Memory.
	linkQuery := `SELECT count(*)
		FROM memory_links AS link
		JOIN sources AS sr ON sr.id = link.source_id`

	sourceArgs := []any{}
	scopedArgs := []any{}
	if scope != "" {
		sourceQuery += ` WHERE sr.scope_value = ?`
		memoryQuery += ` WHERE sr.scope_value = ?`
		linkQuery += ` WHERE sr.scope_value = ?`
		sourceArgs = append(sourceArgs, scope)
		scopedArgs = append(scopedArgs, scope)
	}

	if err := s.db.QueryRowContext(ctx, sourceQuery, sourceArgs...).Scan(&stats.Sources); err != nil {
		return GraphStats{}, fmt.Errorf("count graph sources: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, memoryQuery, scopedArgs...).Scan(&stats.Memories); err != nil {
		return GraphStats{}, fmt.Errorf("count graph memories: %w", err)
	}
	var links int
	if err := s.db.QueryRowContext(ctx, linkQuery, scopedArgs...).Scan(&links); err != nil {
		return GraphStats{}, fmt.Errorf("count graph links: %w", err)
	}
	if stats.Memories > 0 {
		stats.AvgSourcesPerMem = float64(links) / float64(stats.Memories)
	}
	return stats, nil
}

// graphRelations loads the Relation edges (Memory<->Memory, ADR-0007) whose both
// endpoints are among memIDs — the kept Memory nodes — so the graph never
// references a Memory the cap dropped and an out-of-scope Relation target never
// leaks in. It reads memory_relations directly (only "relates" exists today) and
// orders for stable output. An empty memIDs yields no Relations, and a scope with
// no Relations yields an empty slice so the view degrades to provenance-only.
func (s *Store) graphRelations(ctx context.Context, memIDs []string) ([]GraphRelation, error) {
	if len(memIDs) == 0 {
		return []GraphRelation{}, nil
	}

	in := placeholders(len(memIDs))
	query := `SELECT rel.from_memory_id, rel.to_memory_id
		FROM memory_relations AS rel
		WHERE rel.from_memory_id IN (` + in + `)
		AND rel.to_memory_id IN (` + in + `)
		ORDER BY rel.from_memory_id, rel.to_memory_id`
	args := append(stringsToAny(memIDs), stringsToAny(memIDs)...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load graph relations: %w", err)
	}
	defer closeRows(rows)

	relations := []GraphRelation{}
	for rows.Next() {
		var rel GraphRelation
		if err := rows.Scan(&rel.FromMemoryID, &rel.ToMemoryID); err != nil {
			return nil, fmt.Errorf("scan graph relation: %w", err)
		}
		relations = append(relations, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate graph relations: %w", err)
	}
	return relations, nil
}

// relationNeighborRow is one Memory connected to the drilldown center by a
// Relation, carrying the other endpoint's id and label so it can render as a
// Memory node next to the center.
type relationNeighborRow struct {
	ID    string
	Label string
}

// memoryRelationNeighbors loads the Memories directly connected to id by a
// Relation over both directions of memory_relations, with each neighbour's label,
// so the drilldown can expand into connected Memories (ADR-0008), not just derived
// Sources. It is unrestricted by scope, like the rest of the drilldown. Ordered by
// id for stable output.
func (s *Store) memoryRelationNeighbors(ctx context.Context, id MemoryID) ([]relationNeighborRow, error) {
	query := `SELECT DISTINCT nbr.id, nbr.text
		FROM (
			SELECT to_memory_id AS other FROM memory_relations WHERE from_memory_id = ?
			UNION
			SELECT from_memory_id AS other FROM memory_relations WHERE to_memory_id = ?
		) AS rel
		JOIN memories AS nbr ON nbr.id = rel.other
		ORDER BY nbr.id`

	rows, err := s.db.QueryContext(ctx, query, string(id), string(id))
	if err != nil {
		return nil, fmt.Errorf("load memory relation neighbors: %w", err)
	}
	defer closeRows(rows)

	neighbors := []relationNeighborRow{}
	for rows.Next() {
		var row relationNeighborRow
		if err := rows.Scan(&row.ID, &row.Label); err != nil {
			return nil, fmt.Errorf("scan memory relation neighbor: %w", err)
		}
		neighbors = append(neighbors, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory relation neighbors: %w", err)
	}
	return neighbors, nil
}

// MemoryNeighborhood is the click-to-expand drilldown for one Memory: the Memory
// node, every Source that melted into it with their Link edges, and every Memory
// it connects to by a Relation with those Relation edges. It is unrestricted by
// scope (like Get by id), so expanding a Memory reveals its full provenance and
// all its Relations even when the overview was filtered or truncated. found is
// false (with a zero Graph and no error) when no Memory has the id, so the UI
// renders a clean miss. It carries no aggregate Stats — the panel stays the
// scope-level one.
func (s *Store) MemoryNeighborhood(ctx context.Context, id MemoryID) (Graph, bool, error) {
	row, err := queryMemoryRow(ctx, s.db, memoryColumnsSQL()+`
		FROM memories AS m
		WHERE m.id = ?`, string(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Graph{}, false, nil
		}
		return Graph{}, false, fmt.Errorf("load memory for neighborhood: %w", err)
	}
	mem := row.toMemory()

	// Provenance over all scopes: the drilldown shows the Memory's full provenance.
	sourceNodes, edges, _, err := s.graphSourcesAndEdges(ctx, "", []string{string(id)}, defaultGraphNodeCap)
	if err != nil {
		return Graph{}, false, err
	}

	// Connected Memories over all scopes, so drilling down reaches the Relation
	// graph, not only derived Sources.
	neighbors, err := s.memoryRelationNeighbors(ctx, id)
	if err != nil {
		return Graph{}, false, err
	}

	memoryNodes := make([]GraphNode, 0, 1+len(neighbors))
	memoryNodes = append(memoryNodes, GraphNode{
		ID:    string(mem.ID),
		Kind:  GraphNodeMemory,
		Label: mem.Text,
		Size:  len(neighbors),
	})
	relations := make([]GraphRelation, 0, len(neighbors))
	for _, nbr := range neighbors {
		memoryNodes = append(memoryNodes, GraphNode{
			ID:    nbr.ID,
			Kind:  GraphNodeMemory,
			Label: nbr.Label,
		})
		// Direction is not load-bearing for the UI (Relations render undirected);
		// emit from the center for a stable, deduped edge per neighbour.
		relations = append(relations, GraphRelation{FromMemoryID: string(mem.ID), ToMemoryID: nbr.ID})
	}

	graph := Graph{
		Nodes:     append(memoryNodes, sourceNodes...),
		Edges:     edges,
		Relations: relations,
	}
	return graph, true, nil
}
