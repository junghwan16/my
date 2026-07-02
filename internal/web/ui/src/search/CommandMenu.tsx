import { useEffect, useState } from 'react'
import { recall, type RecallResult } from '../api'
import { firstLine, timeAgo } from '../format'
import {
  CommandDialog,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
} from '../components/ui/command'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  scope: string
  onSelect: (memory: RecallResult) => void
}

// CommandMenu is the Cmd-K quick jump: type to recall (server-side ranking),
// pick a memory to open it. cmdk's own filtering is off — the store ranks.
export function CommandMenu({ open, onOpenChange, scope, onSelect }: Props) {
  const [query, setQuery] = useState('')
  const [items, setItems] = useState<RecallResult[]>([])

  useEffect(() => {
    if (!open) return
    let live = true
    const handle = setTimeout(() => {
      recall(query.trim(), scope)
        .then((r) => live && setItems(r))
        .catch(() => live && setItems([]))
    }, 120)
    return () => {
      live = false
      clearTimeout(handle)
    }
  }, [query, scope, open])

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput
        value={query}
        onValueChange={setQuery}
        placeholder="기억을 떠올리기…"
      />
      <CommandList>
        <CommandEmpty>떠오른 기억이 없습니다.</CommandEmpty>
        <CommandGroup>
          {items.map((memory) => (
            <CommandItem
              key={memory.memory_id}
              value={memory.memory_id}
              onSelect={() => onSelect(memory)}
            >
              <span className="min-w-0 flex-1 truncate">{firstLine(memory.text)}</span>
              <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                {memory.agent}
              </span>
              <span className="shrink-0 font-mono text-[10px] text-muted-foreground/70">
                {timeAgo(memory.created_at)}
              </span>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
