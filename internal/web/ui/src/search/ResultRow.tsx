import type { RecallResult } from '../api'
import { firstLine, timeAgo } from '../format'
import { cn } from '../lib/utils'

interface Props {
  result: RecallResult
  query: string
  active: boolean
  onSelect: () => void
}

// Highlight matches of the query within a line, in the accent color (Linear /
// Obsidian "linked mentions" row treatment).
function highlight(line: string, query: string) {
  const q = query.trim()
  if (!q) return line
  const lower = line.toLowerCase()
  const needle = q.toLowerCase()
  const out: React.ReactNode[] = []
  let i = 0
  let key = 0
  while (i < line.length) {
    const at = lower.indexOf(needle, i)
    if (at === -1) {
      out.push(line.slice(i))
      break
    }
    if (at > i) out.push(line.slice(i, at))
    out.push(
      <mark key={key++} className="bg-transparent text-primary">
        {line.slice(at, at + needle.length)}
      </mark>,
    )
    i = at + needle.length
  }
  return out
}

// ResultRow is one dense, keyboard-selectable memory row: a one-line snippet
// with muted agent/kind + time on the right.
export function ResultRow({ result, query, active, onSelect }: Props) {
  return (
    <li>
      <button
        onClick={onSelect}
        className={cn(
          'group flex w-full items-center gap-3 rounded-md px-2.5 py-2 text-left transition-colors',
          active ? 'bg-accent' : 'hover:bg-accent/50',
        )}
      >
        <span
          className={cn(
            'size-1.5 shrink-0 rounded-full',
            active ? 'bg-primary' : 'bg-border group-hover:bg-muted-foreground',
          )}
        />
        <span className="min-w-0 flex-1 truncate text-[13px] text-foreground/90">
          {highlight(firstLine(result.text), query)}
        </span>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {result.agent}
        </span>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground/70">
          {timeAgo(result.created_at)}
        </span>
      </button>
    </li>
  )
}
