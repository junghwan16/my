import type { RecallResult } from '../api'
import { shortId, timeAgo, scopeLabel } from '../format'
import styles from './ResultCard.module.css'

interface Props {
  result: RecallResult
  index: number
}

// ResultCard is one surfaced memory. Its text is the hero; the mono meta line
// carries id / agent / time; provenance names the single Source it melted from
// (a Memory has exactly one Source — CONTEXT.md). A cross-scope recall can
// return no in-scope Source, in which case the provenance line is omitted.
export function ResultCard({ result, index }: Props) {
  const source = result.sources[0]
  // Cap the stagger so a long list never waits on a long tail of delays.
  const step = Math.min(index, 8)

  return (
    <li className={styles.card} style={{ '--i': step } as React.CSSProperties}>
      <p className={styles.text}>{result.text}</p>
      <div className={styles.meta}>
        <span className={styles.id}>{shortId(result.memory_id)}</span>
        <span className={styles.sep}>·</span>
        <span className={styles.agent}>
          {result.agent}/{result.kind}
        </span>
        <span className={styles.sep}>·</span>
        <time dateTime={result.created_at}>{timeAgo(result.created_at)}</time>
      </div>
      {source && (
        <div className={styles.provenance}>
          <span className={styles.tideMark} aria-hidden="true">
            ⌁
          </span>
          원본 {shortId(source.id)} · {scopeLabel(source.scope.value)}
        </div>
      )}
    </li>
  )
}
