import type { ReportPrecheck } from './types.ts'

/**
 * The report trace, read from the precheck the backend already returns.
 *
 * The chain is: 报告 → 结论行(`fact` id) → 事实(`/api/facts/{id}`) → 断言版本与
 * 原始证据. This module only answers two questions the UI used to guess:
 *
 *  - where one fact record lives (a record param on the graph view, so the id is
 *    addressable instead of living only in a click handler);
 *  - what the precheck actually said. A missing `unreported` block is *absent*,
 *    not zero: an older server does not answer it, and printing `0 条` would
 *    claim a fact no report cites when nobody counted.
 */

/** The graph view owns the fact record (`?view=graph&fact=<id>`). */
export const FACT_VIEW = 'graph'

export function factHref(factID: string): string {
  return `?view=${FACT_VIEW}&fact=${encodeURIComponent(factID)}`
}

export interface UnreportedFactRef {
  fact: string
  title: string
}

export interface UnreportedView {
  /** False when the precheck has no `unreported` block at all. */
  present: boolean
  total: number
  items: UnreportedFactRef[]
  /** Rows the server holds beyond the ones it returned. */
  hidden: number
  limit: number
  note: string
  unreadable: string[]
  uncounted: string[]
}

export function unreportedView(precheck?: ReportPrecheck | null): UnreportedView {
  const raw = precheck?.unreported
  if (!raw) {
    return { present: false, total: 0, items: [], hidden: 0, limit: 0, note: '', unreadable: [], uncounted: [] }
  }
  const items = (raw.items ?? [])
    .filter((item) => Boolean(item?.fact))
    .map((item) => ({ fact: item.fact, title: item.title ?? '' }))
  // `total` is the whole set; `items` may be capped by `limit`. Never report
  // fewer rows than were returned, even if a server under-counts.
  const total = Math.max(raw.total ?? items.length, items.length)
  return {
    present: true,
    total,
    items,
    hidden: Math.max(0, total - items.length),
    limit: raw.limit ?? 0,
    note: raw.note ?? '',
    unreadable: (raw.unreadable ?? []).filter(Boolean),
    uncounted: (raw.uncounted ?? []).filter(Boolean),
  }
}

/**
 * What the precheck read amounts to. `unavailable` is its own answer: the
 * console must not treat a failed read as "nothing to warn about" and start a
 * report run as if the check had passed.
 */
export type PrecheckVerdict =
  | { kind: 'loading' }
  | { kind: 'unavailable'; reason: string }
  | { kind: 'running' }
  | { kind: 'ok'; precheck: ReportPrecheck }

export function precheckVerdict(
  precheck: ReportPrecheck | undefined,
  error: string | undefined,
): PrecheckVerdict {
  if (error) return { kind: 'unavailable', reason: error }
  if (!precheck) return { kind: 'loading' }
  if (precheck.running) return { kind: 'running' }
  return { kind: 'ok', precheck }
}
