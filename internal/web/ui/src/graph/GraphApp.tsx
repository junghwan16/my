import { useEffect, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import {
  loadGraph,
  loadScopes,
  expandMemory,
  type GraphStats,
  type Scope,
} from '../api'
import { ScopeSelect } from '../ScopeSelect'
import { shortId, scopeLabel } from '../format'
import { GraphCanvas, toGraphData, type GraphData } from './GraphCanvas'

interface Selection {
  id: string
  kind: string
  label: string
  metric: number
  scope: string
}

// mergeGraphData appends a neighborhood without discarding what's on screen,
// preserving existing node/link object references so the live simulation keeps
// its state (react-force-graph mutates those objects in place).
function mergeGraphData(prev: GraphData, next: GraphData): GraphData {
  const haveNodes = new Set(prev.nodes.map((n) => n.id))
  const haveLinks = new Set(prev.links.map((l) => l.id))
  const nodes = next.nodes.filter((n) => !haveNodes.has(n.id))
  const links = next.links.filter((l) => !haveLinks.has(l.id))
  if (nodes.length === 0 && links.length === 0) return prev
  return { nodes: [...prev.nodes, ...nodes], links: [...prev.links, ...links] }
}

// GraphApp is the depth view: an Obsidian-style force graph for a scope, with an
// aggregate panel (true totals regardless of the node cap) and click-to-expand
// drilldown that appends a Memory's neighborhood.
export function GraphApp() {
  const [scope, setScope] = useState('')
  const [scopes, setScopes] = useState<Scope[]>([])
  const [data, setData] = useState<GraphData>({ nodes: [], links: [] })
  const [stats, setStats] = useState<GraphStats | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [message, setMessage] = useState('그래프를 불러오는 중…')
  const [selection, setSelection] = useState<Selection | null>(null)

  useEffect(() => {
    loadScopes().then(setScopes).catch(() => {})
    fetchGraph('')
  }, [])

  async function fetchGraph(next: string) {
    setMessage('그래프를 불러오는 중…')
    setSelection(null)
    try {
      const graph = await loadGraph(next)
      setData(toGraphData(graph))
      setStats(graph.stats)
      setTruncated(graph.truncated)
      setMessage(
        graph.nodes.length === 0
          ? '이 범위에는 표시할 노드가 없습니다.'
          : `${graph.nodes.length}개 노드 · 드래그하면 딸려오고, 기억을 클릭하면 이웃을 펼칩니다.`,
      )
    } catch {
      setMessage('그래프를 불러오는 중 오류가 발생했습니다.')
    }
  }

  function onScopeChange(next: string) {
    setScope(next)
    fetchGraph(next)
  }

  async function onExpand(memoryId: string) {
    try {
      const graph = await expandMemory(memoryId)
      setData((prev) => mergeGraphData(prev, toGraphData(graph)))
    } catch {
      // A failed drilldown leaves the current graph intact.
    }
  }

  return (
    <div className="flex h-screen flex-col">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border px-4">
        <a href="/" className="flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
          <ArrowLeft className="size-4" />
        </a>
        <div className="flex items-baseline gap-2">
          <span className="text-sm font-semibold tracking-tight">gieok</span>
          <span className="font-mono text-[11px] text-muted-foreground">provenance 그래프</span>
        </div>
        <div className="ml-auto">
          <ScopeSelect scopes={scopes} value={scope} onChange={onScopeChange} />
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <GraphCanvas data={data} onSelect={setSelection} onExpand={onExpand} />

        <aside className="w-72 shrink-0 overflow-y-auto border-l border-border bg-card/40 p-5">
          <h2 className="mb-3 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            집계
          </h2>
          <Stat label="총 원본" value={stats ? String(stats.sources) : '–'} />
          <Stat label="총 기억" value={stats ? String(stats.memories) : '–'} />
          <Stat
            label="평균 원본/기억"
            value={stats ? stats.avg_sources_per_memory.toFixed(2) : '–'}
          />

          <div className="mt-6 space-y-2.5 border-t border-border pt-5">
            <Legend className="rounded-full bg-[#7c8cf0]" label="기억 (Relation degree)" />
            <Legend className="rounded-sm bg-[#3aa8a0]" label="원본 (fan-out)" />
            <Legend dashed label="Relation 기억↔기억" />
          </div>

          {truncated && (
            <p className="mt-5 text-xs leading-relaxed text-amber-400/90">
              노드 상한을 넘어 일부만 표시합니다. 기억을 클릭해 이웃을 펼치세요.
            </p>
          )}

          <p className="mt-5 font-mono text-[11px] leading-relaxed text-muted-foreground">
            {message}
          </p>

          {selection && <SelectionPanel selection={selection} />}
        </aside>
      </div>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between py-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono font-semibold tabular-nums text-primary">{value}</span>
    </div>
  )
}

function Legend({
  className,
  dashed,
  label,
}: {
  className?: string
  dashed?: boolean
  label: string
}) {
  return (
    <div className="flex items-center gap-2.5 text-xs text-muted-foreground">
      {dashed ? (
        <span className="w-4 shrink-0 border-t-2 border-dashed border-[#6f76c9]" />
      ) : (
        <span className={`size-3 shrink-0 ${className}`} />
      )}
      <span>{label}</span>
    </div>
  )
}

function SelectionPanel({ selection }: { selection: Selection }) {
  const memory = selection.kind === 'memory'
  return (
    <div className="mt-5 border-t border-border pt-5">
      <div className="flex items-baseline gap-2">
        <span className="text-sm font-semibold">{memory ? '기억' : '원본'}</span>
        <span className="break-all font-mono text-[11px] text-primary">
          {shortId(selection.id)}
        </span>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {memory
          ? `기억 ${selection.metric}개와 연결`
          : `기억 ${selection.metric}개로 녹음 · ${scopeLabel(selection.scope)}`}
      </p>
      <p className="mt-2 whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/85">
        {selection.label}
      </p>
    </div>
  )
}
