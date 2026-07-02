import { useCallback, useEffect, useRef, useState } from 'react'
import { Command as CommandIcon, Network } from 'lucide-react'
import {
  recall,
  loadScopes,
  expandMemory,
  type RecallResult,
  type Scope,
  type Graph,
} from '../api'
import { ScopeSelect } from '../ScopeSelect'
import { ResultRow } from './ResultRow'
import { MemoryDetail } from './MemoryDetail'
import { CommandMenu } from './CommandMenu'
import { ScrollArea } from '../components/ui/scroll-area'

// SearchApp is the recall workspace: a dense result list on the left, the
// selected Memory with its Source and related memories on the right, and a
// Cmd-K palette for quick jumps. An empty query takes the recent-Memory path.
export function SearchApp() {
  const [query, setQuery] = useState('')
  const [scope, setScope] = useState('')
  const [scopes, setScopes] = useState<Scope[]>([])
  const [results, setResults] = useState<RecallResult[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [neighborhood, setNeighborhood] = useState<Graph | null>(null)
  const [status, setStatus] = useState('불러오는 중…')
  const [paletteOpen, setPaletteOpen] = useState(false)
  const lastQuery = useRef('')

  const selected = results.find((r) => r.memory_id === selectedId) ?? null

  const runRecall = useCallback(async (q: string, s: string, keepSelection = false) => {
    lastQuery.current = q
    setStatus('떠올리는 중…')
    try {
      const memories = await recall(q, s)
      setResults(memories)
      setStatus(
        memories.length === 0
          ? q
            ? '떠오른 기억이 없습니다.'
            : '아직 저장된 기억이 없습니다.'
          : `${memories.length}개의 기억`,
      )
      if (!keepSelection) {
        setSelectedId(memories[0]?.memory_id ?? null)
      }
    } catch {
      setStatus('오류가 발생했습니다.')
    }
  }, [])

  useEffect(() => {
    loadScopes().then(setScopes).catch(() => {})
    runRecall('', '')
  }, [runRecall])

  // Fetch the selected Memory's neighborhood (Source + related memories) so the
  // detail pane can show Roam-style linked references.
  useEffect(() => {
    if (!selectedId) {
      setNeighborhood(null)
      return
    }
    let live = true
    expandMemory(selectedId)
      .then((g) => live && setNeighborhood(g))
      .catch(() => live && setNeighborhood(null))
    return () => {
      live = false
    }
  }, [selectedId])

  // Cmd-K / Ctrl-K opens the palette.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((o) => !o)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  function onScopeChange(next: string) {
    setScope(next)
    runRecall(lastQuery.current, next)
  }

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    runRecall(query.trim(), scope)
  }

  // Selecting from the palette: ensure the memory is in the list, then select.
  function onPaletteSelect(memory: RecallResult) {
    setResults((prev) =>
      prev.some((r) => r.memory_id === memory.memory_id) ? prev : [memory, ...prev],
    )
    setSelectedId(memory.memory_id)
    setPaletteOpen(false)
  }

  function onMemoryUpdated(updated: RecallResult) {
    setResults((prev) =>
      prev.map((r) => (r.memory_id === updated.memory_id ? updated : r)),
    )
  }

  return (
    <div className="h-screen bg-[#08080b]">
      {/* Contained app: capped width + centered so it never stretches thin on a
          wide monitor; the darker gutters read as an intentional app frame. */}
      <div className="mx-auto flex h-full max-w-[1180px] flex-col border-x border-border bg-background">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border px-4">
        <div className="flex items-baseline gap-2">
          <span className="text-sm font-semibold tracking-tight">gieok</span>
          <span className="font-mono text-[11px] text-muted-foreground">기억</span>
        </div>
        <button
          onClick={() => setPaletteOpen(true)}
          className="ml-2 flex h-8 items-center gap-2 rounded-md border border-border bg-secondary/40 px-2.5 text-xs text-muted-foreground transition-colors hover:border-primary/50 hover:text-foreground"
        >
          <CommandIcon className="size-3.5" />
          빠른 검색
          <kbd className="ml-1 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">⌘K</kbd>
        </button>
        <div className="ml-auto flex items-center gap-3">
          <ScopeSelect scopes={scopes} value={scope} onChange={onScopeChange} />
          <a
            href="/graph"
            className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <Network className="size-3.5" />
            그래프
          </a>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* Left: search + dense result list. */}
        <div className="flex w-[40%] min-w-[320px] max-w-[560px] flex-col border-r border-border">
          <form onSubmit={onSubmit} className="shrink-0 border-b border-border p-3">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="무엇을 떠올릴까요?"
              autoComplete="off"
              autoFocus
              aria-label="떠올릴 내용"
              className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
            />
            <div className="mt-2 px-0.5 font-mono text-[11px] text-muted-foreground">
              {status}
            </div>
          </form>
          <ScrollArea className="flex-1">
            <ul className="p-1.5">
              {results.map((memory) => (
                <ResultRow
                  key={memory.memory_id}
                  result={memory}
                  query={lastQuery.current}
                  active={memory.memory_id === selectedId}
                  onSelect={() => setSelectedId(memory.memory_id)}
                />
              ))}
            </ul>
          </ScrollArea>
        </div>

        {/* Right: selected memory detail with linked references. */}
        <div className="min-w-0 flex-1">
          <MemoryDetail
            memory={selected}
            neighborhood={neighborhood}
            onSelectRelated={(id) => setSelectedId(id)}
            onUpdated={onMemoryUpdated}
          />
        </div>
      </div>
      </div>

      <CommandMenu
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        scope={scope}
        onSelect={onPaletteSelect}
      />
    </div>
  )
}
