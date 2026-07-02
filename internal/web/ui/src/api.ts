// api.ts mirrors the Go JSON contract (internal/web/server.go) and wraps the
// four read-only endpoints. Field names match the Go json tags exactly so the
// types stay a faithful shadow of the server response.

export interface Scope {
  kind: string
  value: string
}

export interface SourceRef {
  id: string
  uri: string
  scope: Scope
}

export interface RecallResult {
  memory_id: string
  agent: string
  kind: string
  // Effective text: the human Override if one exists, otherwise the agent's
  // original memory text (ADR-0010).
  text: string
  created_at: string
  // edited is true when a human Override is layered over the agent's memory;
  // original_text carries the agent's text so the UI can show/restore it.
  edited?: boolean
  original_text?: string
  // A Memory derives from exactly one Source (CONTEXT.md, ADR-0008), so this is
  // length 0 (source outside the recalled scope) or 1 in practice.
  sources: SourceRef[]
}

export type GraphNodeKind = 'source' | 'memory'

export interface GraphNode {
  id: string
  kind: GraphNodeKind
  label: string
  size: number
  scope: Scope
}

export interface GraphEdge {
  source_id: string
  memory_id: string
}

export interface GraphRelation {
  from_memory_id: string
  to_memory_id: string
}

export interface GraphStats {
  sources: number
  memories: number
  avg_sources_per_memory: number
}

export interface Graph {
  nodes: GraphNode[]
  edges: GraphEdge[]
  relations: GraphRelation[]
  stats: GraphStats
  truncated: boolean
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`요청 실패 (${res.status})`)
  return (await res.json()) as T
}

// recall runs the shared recall seam. Scope is always sent explicitly: an empty
// value is the all-scopes choice, a non-empty value filters to that Scope. A
// positive limit caps the result count; 0 uses the store default.
export async function recall(query: string, scope: string, limit = 0): Promise<RecallResult[]> {
  const limitParam = limit > 0 ? `&limit=${limit}` : ''
  const data = await getJSON<{ memories: RecallResult[] }>(
    `/api/recall?query=${encodeURIComponent(query)}&scope=${encodeURIComponent(scope)}${limitParam}`,
  )
  return data.memories ?? []
}

export async function loadScopes(): Promise<Scope[]> {
  const data = await getJSON<{ scopes: Scope[] }>('/api/scopes')
  return data.scopes ?? []
}

export async function loadGraph(scope: string): Promise<Graph> {
  return getJSON<Graph>(`/api/graph?scope=${encodeURIComponent(scope)}`)
}

// getMemory returns one Memory by id, so the graph can deep-link a node into the
// recall view.
export async function getMemory(id: string): Promise<RecallResult> {
  return getJSON<RecallResult>(`/api/memory?id=${encodeURIComponent(id)}`)
}

// expandMemory fetches one Memory's full neighborhood (its Source and connected
// Memories), unrestricted by scope, for click-to-expand drilldown.
export async function expandMemory(id: string): Promise<Graph> {
  return getJSON<Graph>(`/api/graph/memory?id=${encodeURIComponent(id)}`)
}

async function sendJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`요청 실패 (${res.status})`)
  return (await res.json()) as T
}

// editMemory layers a human Override on a Memory without changing its identity
// or provenance (ADR-0010). An empty text clears the Override, restoring the
// agent's original memory. Returns the updated result.
export async function editMemory(id: string, text: string): Promise<RecallResult> {
  return sendJSON<RecallResult>('/api/memory/edit', { memory_id: id, text })
}
