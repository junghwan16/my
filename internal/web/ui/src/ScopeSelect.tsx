import { ChevronsUpDown } from 'lucide-react'
import type { Scope } from './api'
import { scopeLabel } from './format'
import { cn } from './lib/utils'

interface Props {
  scopes: Scope[]
  value: string
  onChange: (value: string) => void
  className?: string
}

// ScopeSelect is a native <select> — accessible and keyboard-friendly for free
// (ADR-0009). The all-scopes choice is the empty value; the store never returns
// it, so we prepend it here.
export function ScopeSelect({ scopes, value, onChange, className }: Props) {
  return (
    <div className={cn('relative inline-flex items-center', className)}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label="범위"
        className="h-8 max-w-56 appearance-none rounded-md border border-border bg-secondary/50 pl-3 pr-8 font-mono text-xs text-foreground outline-none hover:border-primary/60 focus-visible:ring-2 focus-visible:ring-ring"
      >
        <option value="">모든 범위</option>
        {scopes.map((scope) => (
          <option key={scope.value} value={scope.value}>
            {scopeLabel(scope.value)}
          </option>
        ))}
      </select>
      <ChevronsUpDown className="pointer-events-none absolute right-2 size-3.5 text-muted-foreground" />
    </div>
  )
}
