import { useEffect, useState } from 'react'
import { ArrowLeft, MousePointerClick } from 'lucide-react'
import { loadGraph, loadScopes, type GraphStats, type Scope } from '../api'
import { ScopeSelect } from '../ScopeSelect'
import { shortId, scopeLabel } from '../format'
import { GraphCanvas, toGraphData, type GraphData, type NodeInfo } from './GraphCanvas'

// GraphApp is the overview + jump-off: an Obsidian-style force graph of a scope
// where hovering previews a node and clicking opens it in the recall view —
// a memory jumps to that memory, a source to its scope's recall.
export function GraphApp() {
  const [scope, setScope] = useState('')
  const [scopes, setScopes] = useState<Scope[]>([])
  const [data, setData] = useState<GraphData>({ nodes: [], links: [] })
  const [stats, setStats] = useState<GraphStats | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [message, setMessage] = useState('그래프를 불러오는 중…')
  const [hovered, setHovered] = useState<NodeInfo | null>(null)

  useEffect(() => {
    loadScopes().then(setScopes).catch(() => {})
    fetchGraph('')
  }, [])

  async function fetchGraph(next: string) {
    setMessage('그래프를 불러오는 중…')
    setHovered(null)
    try {
      const graph = await loadGraph(next)
      setData(toGraphData(graph))
      setStats(graph.stats)
      setTruncated(graph.truncated)
      setMessage(
        graph.nodes.length === 0
          ? '이 범위에는 표시할 노드가 없습니다.'
          : `${graph.nodes.length}개 노드`,
      )
    } catch {
      setMessage('그래프를 불러오는 중 오류가 발생했습니다.')
    }
  }

  function onOpen(node: NodeInfo) {
    if (node.kind === 'memory') {
      window.location.href = `/?m=${encodeURIComponent(node.id)}`
    } else {
      window.location.href = `/?scope=${encodeURIComponent(node.scope)}`
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
          <ScopeSelect
            scopes={scopes}
            value={scope}
            onChange={(next) => {
              setScope(next)
              fetchGraph(next)
            }}
          />
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <GraphCanvas data={data} onHoverNode={setHovered} onOpen={onOpen} />

        <aside className="flex w-72 shrink-0 flex-col overflow-y-auto border-l border-border bg-card/40 p-5">
          <h2 className="mb-3 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            집계
          </h2>
          <Stat label="총 원본" value={stats ? String(stats.sources) : '–'} />
          <Stat label="총 기억" value={stats ? String(stats.memories) : '–'} />
          <Stat label="평균 원본/기억" value={stats ? stats.avg_sources_per_memory.toFixed(2) : '–'} />

          <div className="mt-6 space-y-2.5 border-t border-border pt-5">
            <Legend className="rounded-full bg-[#7c8cf0]" label="기억 (Relation degree)" />
            <Legend className="rounded-sm bg-[#3aa8a0]" label="원본 (fan-out)" />
            <Legend dashed label="Relation 기억↔기억" />
          </div>

          <div className="mt-5 flex items-start gap-2 rounded-md bg-secondary/40 p-3 text-xs text-muted-foreground">
            <MousePointerClick className="mt-0.5 size-3.5 shrink-0" />
            <span>노드를 클릭하면 그 기억으로 이동합니다. 드래그로 흩고, 휠로 확대·축소.</span>
          </div>

          {truncated && (
            <p className="mt-4 text-xs leading-relaxed text-amber-400/90">
              노드 상한을 넘어 일부만 표시합니다.
            </p>
          )}

          <p className="mt-4 font-mono text-[11px] text-muted-foreground">{message}</p>

          {/* Hover preview so you can read a node before clicking through. */}
          {hovered && (
            <div className="mt-auto border-t border-border pt-4">
              <div className="flex items-baseline gap-2">
                <span className="text-sm font-semibold">{hovered.kind === 'memory' ? '기억' : '원본'}</span>
                <span className="break-all font-mono text-[11px] text-primary">{shortId(hovered.id)}</span>
              </div>
              <p className="mt-1.5 text-[11px] text-muted-foreground">
                {hovered.kind === 'memory'
                  ? `기억 ${hovered.metric}개와 연결`
                  : `기억 ${hovered.metric}개로 녹음 · ${scopeLabel(hovered.scope)}`}
              </p>
              <p className="mt-2 line-clamp-6 whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/85">
                {hovered.label}
              </p>
              <p className="mt-2 font-mono text-[10px] text-primary">클릭하면 열림 →</p>
            </div>
          )}
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

function Legend({ className, dashed, label }: { className?: string; dashed?: boolean; label: string }) {
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
