// Presentation helpers shared by both pages.

// timeAgo renders an ISO timestamp as a short Korean relative time, falling back
// to an absolute date past a week so old memories still read cleanly.
export function timeAgo(iso: string): string {
  const then = new Date(iso)
  if (Number.isNaN(then.getTime())) return ''
  const seconds = Math.round((Date.now() - then.getTime()) / 1000)
  if (seconds < 60) return '방금'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}분 전`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}시간 전`
  const days = Math.round(hours / 24)
  if (days < 7) return `${days}일 전`
  return then.toLocaleDateString('ko-KR', { year: 'numeric', month: 'short', day: 'numeric' })
}

// shortId trims a hashed id ("memory:ab34…", "source:cd12…") to a scannable
// stub while keeping the family prefix.
export function shortId(id: string, keep = 8): string {
  const colon = id.indexOf(':')
  if (colon === -1) return id.length > keep ? id.slice(0, keep) + '…' : id
  const prefix = id.slice(0, colon + 1)
  const rest = id.slice(colon + 1)
  return rest.length > keep ? prefix + rest.slice(0, keep) + '…' : id
}

// firstLine returns the first non-empty line of a memory, for dense list rows.
export function firstLine(text: string): string {
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (trimmed) return trimmed
  }
  return text.trim()
}

// scopeLabel renders a Scope for display; a path-like value shows its last two
// segments so long workspace paths stay legible.
export function scopeLabel(value: string): string {
  if (!value) return '모든 범위'
  const parts = value.split('/').filter(Boolean)
  if (parts.length <= 2) return value
  return '…/' + parts.slice(-2).join('/')
}
