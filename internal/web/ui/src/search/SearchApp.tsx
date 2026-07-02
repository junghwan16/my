import { useCallback, useEffect, useRef, useState } from 'react'
import { Network } from 'lucide-react'
import { recall, loadScopes, type RecallResult, type Scope } from '../api'
import { ScopeSelect } from '../ScopeSelect'
import { MemoryCard } from './MemoryCard'

// SearchApp is the recall surface: type a task, read the memories it surfaces.
// Memories are short, so results are shown as a single reading column of full
// cards rather than a master/detail split. An empty query returns recent memory.
export function SearchApp() {
  const [query, setQuery] = useState('')
  const [scope, setScope] = useState('')
  const [scopes, setScopes] = useState<Scope[]>([])
  const [results, setResults] = useState<RecallResult[]>([])
  const [status, setStatus] = useState('불러오는 중…')
  const lastQuery = useRef('')

  const runRecall = useCallback(async (q: string, s: string) => {
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
    } catch {
      setStatus('오류가 발생했습니다.')
    }
  }, [])

  useEffect(() => {
    loadScopes().then(setScopes).catch(() => {})
    runRecall('', '')
  }, [runRecall])

  function onUpdated(updated: RecallResult) {
    setResults((prev) => prev.map((r) => (r.memory_id === updated.memory_id ? updated : r)))
  }

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 flex h-12 items-center gap-3 border-b border-border bg-background/85 px-5 backdrop-blur">
        <div className="flex items-baseline gap-2">
          <span className="text-sm font-semibold tracking-tight">gieok</span>
          <span className="font-mono text-[11px] text-muted-foreground">기억</span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <ScopeSelect
            scopes={scopes}
            value={scope}
            onChange={(next) => {
              setScope(next)
              runRecall(lastQuery.current, next)
            }}
          />
          <a
            href="/graph"
            className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <Network className="size-3.5" />
            그래프
          </a>
        </div>
      </header>

      <main className="mx-auto max-w-3xl px-5 py-7">
        <form
          onSubmit={(e) => {
            e.preventDefault()
            runRecall(query.trim(), scope)
          }}
        >
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="무엇을 떠올릴까요?"
            autoComplete="off"
            autoFocus
            aria-label="떠올릴 내용"
            className="h-11 w-full rounded-lg border border-input bg-transparent px-4 text-[15px] outline-none placeholder:text-muted-foreground focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-ring"
          />
        </form>

        <p className="mt-3 px-1 font-mono text-[11px] text-muted-foreground">{status}</p>

        <ul className="mt-3 flex flex-col gap-3">
          {results.map((memory) => (
            <MemoryCard
              key={memory.memory_id}
              result={memory}
              query={lastQuery.current}
              onUpdated={onUpdated}
            />
          ))}
        </ul>
      </main>
    </div>
  )
}
