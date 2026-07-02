import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, RotateCcw, FileText, ArrowUpRight, Link2 } from 'lucide-react'
import { editMemory, type Graph, type RecallResult } from '../api'
import { shortId, timeAgo, scopeLabel, firstLine } from '../format'
import { Button } from '../components/ui/button'
import { Badge } from '../components/ui/badge'
import { Separator } from '../components/ui/separator'
import { Textarea } from '../components/ui/textarea'
import { ScrollArea } from '../components/ui/scroll-area'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog'

interface Props {
  memory: RecallResult | null
  neighborhood: Graph | null
  onSelectRelated: (id: string) => void
  onUpdated: (updated: RecallResult) => void
}

export function MemoryDetail({ memory, neighborhood, onSelectRelated, onUpdated }: Props) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [showOriginal, setShowOriginal] = useState(false)

  // Reset the editor whenever the selected memory changes.
  useEffect(() => {
    setEditing(false)
    setShowOriginal(false)
  }, [memory?.memory_id])

  if (!memory) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-sm text-muted-foreground">
        왼쪽에서 기억을 선택하세요.
      </div>
    )
  }

  const source = memory.sources[0]
  const related = (neighborhood?.nodes ?? []).filter(
    (n) => n.kind === 'memory' && n.id !== memory.memory_id,
  )

  async function save() {
    setSaving(true)
    try {
      const updated = await editMemory(memory!.memory_id, draft)
      onUpdated(updated)
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
      const updated = await editMemory(memory!.memory_id, '')
      onUpdated(updated)
      toast.success('원본으로 되돌렸습니다.')
    } catch {
      toast.error('되돌리지 못했습니다.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <ScrollArea className="h-full">
      <article className="mx-auto max-w-2xl px-8 py-7">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-primary">{shortId(memory.memory_id, 12)}</span>
          <Badge variant="outline">{memory.agent}</Badge>
          <Badge variant="outline">{memory.kind}</Badge>
          <span className="font-mono text-[11px] text-muted-foreground">
            {timeAgo(memory.created_at)}
          </span>
          {memory.edited && (
            <Badge variant="accent" className="gap-1">
              <Pencil className="size-3" /> 편집됨
            </Badge>
          )}
        </div>

        <div className="mt-5">
          {editing ? (
            <div>
              <Textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                rows={8}
                autoFocus
                className="font-sans text-[15px] leading-relaxed"
              />
              <div className="mt-3 flex items-center gap-2">
                <Button size="sm" onClick={save} disabled={saving || !draft.trim()}>
                  저장
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing(false)} disabled={saving}>
                  취소
                </Button>
                <span className="ml-auto text-xs text-muted-foreground">
                  원본은 보존되고 편집본이 위에 얹힙니다.
                </span>
              </div>
            </div>
          ) : (
            <>
              <p className="whitespace-pre-wrap text-[15px] leading-relaxed text-foreground/95">
                {memory.text}
              </p>
              <div className="mt-4 flex items-center gap-1.5">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setDraft(memory.text)
                    setEditing(true)
                  }}
                >
                  <Pencil /> 편집
                </Button>
                {memory.edited && (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => setShowOriginal(true)}>
                      <FileText /> 원본 보기
                    </Button>
                    <Button size="sm" variant="ghost" onClick={revert} disabled={saving}>
                      <RotateCcw /> 되돌리기
                    </Button>
                  </>
                )}
              </div>
            </>
          )}
        </div>

        <Separator className="my-7" />

        <section>
          <h2 className="mb-2.5 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            <Link2 className="size-3.5" /> 원본
          </h2>
          {source ? (
            <div className="rounded-lg border border-border bg-card/50 p-3">
              <div className="font-mono text-xs text-foreground/90">{source.id}</div>
              <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                {scopeLabel(source.scope.value)}
              </div>
              {source.uri && (
                <div className="mt-1 truncate font-mono text-[11px] text-muted-foreground/70">
                  {source.uri}
                </div>
              )}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">이 범위에 표시할 원본이 없습니다.</p>
          )}
        </section>

        <section className="mt-6">
          <h2 className="mb-2.5 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            <ArrowUpRight className="size-3.5" /> 연결된 기억 {related.length > 0 && `· ${related.length}`}
          </h2>
          {related.length > 0 ? (
            <ul className="space-y-1">
              {related.map((node) => (
                <li key={node.id}>
                  <button
                    onClick={() => onSelectRelated(node.id)}
                    className="flex w-full items-center gap-2 rounded-md border border-transparent px-2.5 py-2 text-left transition-colors hover:border-border hover:bg-accent/50"
                  >
                    <span className="size-1.5 shrink-0 rounded-full bg-primary/70" />
                    <span className="min-w-0 flex-1 truncate text-[13px] text-foreground/85">
                      {firstLine(node.label)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted-foreground">아직 연결된 기억이 없습니다.</p>
          )}
        </section>
      </article>

      <Dialog open={showOriginal} onOpenChange={setShowOriginal}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>원본 기억 (에이전트)</DialogTitle>
          </DialogHeader>
          <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground/90">
            {memory.original_text}
          </p>
        </DialogContent>
      </Dialog>
    </ScrollArea>
  )
}
