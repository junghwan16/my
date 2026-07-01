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

// GraphNode is one node in the provenance graph: a Source or a Memory. FanIn is
// meaningful only for Memory nodes — it is the number of in-scope Sources that
// melted into that Memory (its Link fan-in), which the UI turns into node
// size/badge. For Source nodes FanIn is zero. Scope carries a Source node's
// workspace so the UI can label provenance; it is empty for Memory nodes, which
// span whichever Sources they derive from.
type GraphNode struct {
	ID    string           `json:"id"`
	Kind  GraphNodeKind    `json:"kind"`
	Label string           `json:"label"`
	FanIn int              `json:"fan_in"`
	Scope sourcespkg.Scope `json:"scope"`
}

// GraphEdge is one Link edge (Source->Memory provenance) in the graph. Relation
// edges (Memory<->Memory) are a later slice (#19) and are not emitted here.
type GraphEdge struct {
	SourceID string `json:"source_id"`
	MemoryID string `json:"memory_id"`
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

// Graph is the provenance graph for one scope: Source and Memory nodes with
// Link (Source->Memory) edges, plus the aggregate panel. Truncated reports
// whether the node cap dropped part of the scope, so the UI can tell the user
// the overview is partial and to drill down for the rest. Nodes and Edges are
// never nil (empty slices for an empty store or empty scope), so the JSON
// contract is stable.
type Graph struct {
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Stats     GraphStats  `json:"stats"`
	Truncated bool        `json:"truncated"`
}

// Graph builds the provenance graph for a scope: the Source and Memory nodes and
// the Link edges between them, each Memory sized by its in-scope Link fan-in. An
// empty scope spans every scope. It is a read-model query for the web /graph
// page and never touches the recall path.
//
// Scoping mirrors recall's no-leak rule: a Source belongs to the scope when its
// scope_value matches; a Memory belongs when it links to at least one in-scope
// Source; and a Link edge is emitted only when both its endpoints are in scope,
// so a Memory's out-of-scope provenance never leaks into a scoped view. Fan-in
// counts only in-scope Sources for the same reason.
//
// cap bounds the returned node count so a browser graph stays renderable; a
// non-positive cap falls back to defaultGraphNodeCap. When the scope holds more
// nodes than the cap, the densest Memories (highest fan-in first) and the
// Sources they derive from are kept, Truncated is set, and the rest is reached
// by MemoryNeighborhood drilldown. The aggregate Stats are always computed over
// the whole scope, independent of the cap.
func (s *Store) Graph(ctx context.Context, scope string, cap int) (Graph, error) {
	if cap <= 0 {
		cap = defaultGraphNodeCap
	}

	stats, err := s.graphStats(ctx, scope)
	if err != nil {
		return Graph{}, err
	}

	fanIn, err := s.memoryFanIn(ctx, scope)
	if err != nil {
		return Graph{}, err
	}

	// Keep the densest Memories first (highest fan-in, then id for stable
	// output), then take Sources they link to, until the cap is reached. This
	// keeps a truncated overview centered on the most-melted Memories rather
	// than an arbitrary slice.
	graph := Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, Stats: stats}

	memNodes, memIDs, memTruncated := selectMemoryNodes(fanIn, cap)

	sourceNodes, edges, srcTruncated, err := s.graphSourcesAndEdges(ctx, scope, memIDs, cap-len(memNodes))
	if err != nil {
		return Graph{}, err
	}

	graph.Truncated = memTruncated || srcTruncated
	graph.Nodes = append(graph.Nodes, memNodes...)
	graph.Nodes = append(graph.Nodes, sourceNodes...)
	graph.Edges = edges
	return graph, nil
}

// memoryFanInRow pairs an in-scope Memory with its label text and in-scope Link
// fan-in, ordered densest-first so the cap keeps the most-melted Memories.
type memoryFanInRow struct {
	ID    string
	Label string
	FanIn int
}

// memoryFanIn loads every in-scope Memory with the count of distinct in-scope
// Sources that melted into it (its Link fan-in), ordered by fan-in desc then id
// for stable, densest-first truncation. An empty scope spans every scope.
func (s *Store) memoryFanIn(ctx context.Context, scope string) ([]memoryFanInRow, error) {
	query := `SELECT m.id, m.text, count(DISTINCT link.source_id) AS fan_in
		FROM memories AS m
		JOIN memory_links AS link ON link.memory_id = m.id
		JOIN sources AS sr ON sr.id = link.source_id`
	args := []any{}
	if scope != "" {
		query += `
		WHERE sr.scope_value = ?`
		args = append(args, scope)
	}
	query += `
		GROUP BY m.id
		ORDER BY fan_in DESC, m.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load memory fan-in: %w", err)
	}
	defer closeRows(rows)

	fanIn := []memoryFanInRow{}
	for rows.Next() {
		var row memoryFanInRow
		if err := rows.Scan(&row.ID, &row.Label, &row.FanIn); err != nil {
			return nil, fmt.Errorf("scan memory fan-in: %w", err)
		}
		fanIn = append(fanIn, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory fan-in: %w", err)
	}
	return fanIn, nil
}

// selectMemoryNodes turns the densest-first fan-in rows into Memory nodes up to
// half the cap, so both Memory and Source nodes fit under the budget. It reports
// which Memory ids were kept (to scope the edge query) and whether any Memory
// was dropped. A Memory node's label is its text, sized by fan-in in the UI.
func selectMemoryNodes(fanIn []memoryFanInRow, cap int) ([]GraphNode, []string, bool) {
	// Reserve at most half the budget for Memory nodes so their Sources also
	// fit; a Memory with no room is dropped and reached by drilldown.
	memBudget := cap / 2
	if memBudget < 1 && cap >= 1 {
		memBudget = 1
	}

	limit := len(fanIn)
	truncated := false
	if limit > memBudget {
		limit = memBudget
		truncated = true
	}

	nodes := make([]GraphNode, 0, limit)
	ids := make([]string, 0, limit)
	for _, row := range fanIn[:limit] {
		nodes = append(nodes, GraphNode{
			ID:    row.ID,
			Kind:  GraphNodeMemory,
			Label: row.Label,
			FanIn: row.FanIn,
		})
		ids = append(ids, row.ID)
	}
	return nodes, ids, truncated
}

// graphSourcesAndEdges loads the Source nodes and Link edges for the kept
// Memories: every in-scope Source that melted into one of memIDs, and the
// (Source, Memory) Link between them. It caps the Source nodes at budget so the
// whole graph stays under the node cap; edges to a dropped Source are omitted so
// the graph never references a missing node, and truncated reports whether any
// Source was dropped. An empty memIDs yields no sources and no edges.
func (s *Store) graphSourcesAndEdges(ctx context.Context, scope string, memIDs []string, budget int) (_ []GraphNode, _ []GraphEdge, truncated bool, _ error) {
	if len(memIDs) == 0 || budget <= 0 {
		return []GraphNode{}, []GraphEdge{}, len(memIDs) > 0, nil
	}

	query := `SELECT sr.id, sr.scope_kind, sr.scope_value, link.memory_id
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
		if err := rows.Scan(&srcID, &scopeKind, &scopeValue, &memoryID); err != nil {
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

// MemoryNeighborhood is the click-to-expand drilldown for one Memory: the Memory
// node plus every Source that melted into it and the Link edges between them. It
// is unrestricted by scope (like Get by id), so expanding a Memory reveals all
// of its provenance even when the overview was filtered. found is false (with a
// zero Graph and no error) when no Memory has the id, so the UI renders a clean
// miss. It carries no aggregate Stats — the panel stays the scope-level one.
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

	// Fan-in over all scopes: the drilldown shows the Memory's full provenance.
	sourceNodes, edges, _, err := s.graphSourcesAndEdges(ctx, "", []string{string(id)}, defaultGraphNodeCap)
	if err != nil {
		return Graph{}, false, err
	}

	graph := Graph{
		Nodes: append([]GraphNode{{
			ID:    string(mem.ID),
			Kind:  GraphNodeMemory,
			Label: mem.Text,
			FanIn: len(sourceNodes),
		}}, sourceNodes...),
		Edges: edges,
	}
	return graph, true, nil
}
