import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, RotateCcw, FileText } from 'lucide-react'
import { editMemory, type RecallResult } from '../api'
import { shortId, timeAgo, scopeLabel } from '../format'
import { Button } from '../components/ui/button'
import { Badge } from '../components/ui/badge'
import { Textarea } from '../components/ui/textarea'
import { cn } from '../lib/utils'

// Highlight matches of the query within the text, in the accent color.
function highlight(text: string, query: string) {
  const q = query.trim()
  if (!q) return text
  const lower = text.toLowerCase()
  const needle = q.toLowerCase()
  const out: React.ReactNode[] = []
  let i = 0
  let key = 0
  while (i < text.length) {
    const at = lower.indexOf(needle, i)
    if (at === -1) {
      out.push(text.slice(i))
      break
    }
    if (at > i) out.push(text.slice(i, at))
    out.push(
      <mark key={key++} className="rounded-sm bg-primary/20 px-0.5 text-foreground">
        {text.slice(at, at + needle.length)}
      </mark>,
    )
    i = at + needle.length
  }
  return out
}

interface Props {
  result: RecallResult
  query: string
  onUpdated: (updated: RecallResult) => void
}

// MemoryCard shows one recalled memory. Long text is clamped to a scannable
// snippet with a "더 보기" toggle so a list of results stays skimmable; short
// memories show in full. A mono meta line carries id / agent / time / provenance,
// and editing layers a non-destructive Override (ADR-0010).
export function MemoryCard({ result, query, onUpdated }: Props) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [showOriginal, setShowOriginal] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [clamped, setClamped] = useState(false)
  const textRef = useRef<HTMLParagraphElement>(null)
  const source = result.sources[0]

  // Detect whether the snippet is actually truncated, so the toggle only shows
  // when there's more to read.
  useEffect(() => {
    const el = textRef.current
    if (!el || expanded) return
    setClamped(el.scrollHeight - el.clientHeight > 4)
  }, [result.text, expanded])

  async function save() {
    setSaving(true)
    try {
      onUpdated(await editMemory(result.memory_id, draft))
      setEditing(false)
      toast.success('기억을 편집했습니다.')
    } catch {
      toast.error('편집을 저장하지 못했습니다.')
    } finally {
      setSaving(false)
    }
  }

  async function revert() {
    setSaving(true)
    try {
      onUpdated(await editMemory(result.memory_id, ''))
      toast.success('원본으로 되돌렸습니다.')
    } catch {
      toast.error('되돌리지 못했습니다.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <li className="group rounded-lg border border-border bg-card/40 p-4 transition-colors hover:border-border/80">
      {editing ? (
        <div>
          <Textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={8}
            autoFocus
            className="text-[15px] leading-relaxed"
          />
          <div className="mt-2 flex items-center gap-2">
            <Button size="sm" onClick={save} disabled={saving || !draft.trim()}>
              저장
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)} disabled={saving}>
              취소
            </Button>
            <span className="ml-auto text-xs text-muted-foreground">원본은 보존됩니다</span>
          </div>
        </div>
      ) : (
        <>
          <p
            ref={textRef}
            className={cn(
              'whitespace-pre-wrap text-[15px] leading-relaxed text-foreground/95',
              !expanded && 'line-clamp-4',
            )}
          >
            {highlight(result.text, query)}
          </p>
          {(clamped || expanded) && (
            <button
              onClick={() => setExpanded((v) => !v)}
              className="mt-1.5 text-xs font-medium text-primary hover:underline"
            >
              {expanded ? '접기' : '더 보기'}
            </button>
          )}
        </>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-muted-foreground">
        <span className="text-primary">{shortId(result.memory_id, 10)}</span>
        <span className="opacity-40">·</span>
        <span>
          {result.agent}/{result.kind}
        </span>
        <span className="opacity-40">·</span>
        <time dateTime={result.created_at}>{timeAgo(result.created_at)}</time>
        {result.edited && (
          <Badge variant="accent" className="ml-1 gap-1 py-0">
            <Pencil className="size-2.5" /> 편집됨
          </Badge>
        )}
        {source && (
          <>
            <span className="opacity-40">·</span>
            <span className="text-muted-foreground/80">
              원본 {shortId(source.id, 10)} · {scopeLabel(source.scope.value)}
            </span>
          </>
        )}
        {!editing && (
          <span className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2"
              onClick={() => {
                setDraft(result.text)
                setEditing(true)
              }}
            >
              <Pencil /> 편집
            </Button>
            {result.edited && (
              <>
                <Button size="sm" variant="ghost" className="h-6 px-2" onClick={() => setShowOriginal((v) => !v)}>
                  <FileText /> 원본
                </Button>
                <Button size="sm" variant="ghost" className="h-6 px-2" onClick={revert} disabled={saving}>
                  <RotateCcw /> 되돌리기
                </Button>
              </>
            )}
          </span>
        )}
      </div>

      {showOriginal && result.original_text && (
        <p className="mt-3 whitespace-pre-wrap border-t border-border pt-3 text-[13px] leading-relaxed text-muted-foreground">
          <span className="mb-1 block font-mono text-[10px] uppercase tracking-wide">원본 (에이전트)</span>
          {result.original_text}
        </p>
      )}
    </li>
  )
}
