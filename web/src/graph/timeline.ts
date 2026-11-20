// The graph's time axis.
//
// The axis is the ledger's own order, not wall-clock distance: a project's rows
// are sparse and bursty (most of a 500-row engagement lands inside one hour), so
// equal time slices would leave the slider doing nothing for half its travel and
// then jumping through everything at once. The slider therefore moves one *row*
// per step, and the clock labels are read off the row under the cursor.
//
// The only ordering key is `created_at` — every fact, direction and goal carries
// it, it is the ledger's record time, and it is what the existing time-window
// filter already uses. `observed_at` is excluded: it is the writer's claim about
// when it saw the thing, it may be missing or wrong, and it is inside the claim
// hash, so a correction would re-order history the ledger already settled.
//
// Rows without a usable timestamp are never hidden by the axis (the same rule as
// the time-window filter): they are counted as `untimed` and always drawn. A
// slider that silently swallowed them would delete evidence for being undated.

import type { GraphNode } from './model'

export interface GraphTimeline {
  /** Node ids in ledger order: created_at, then id. Excludes undated rows. */
  order: string[]
  /** The row's ledger time, aligned with `order` (ISO as recorded). */
  times: string[]
  /** node id → position in `order`. Undated ids are absent on purpose. */
  rank: Map<string, number>
  /** Drawn nodes with no usable timestamp: always visible, counted separately. */
  untimed: string[]
}

/** Structural nodes carry no ledger time at all: they are never gated. */
function undatedByType(type: GraphNode['type']): boolean {
  return type === 'project' || type === 'boundary'
}

/** Parse `created_at` into a comparable millisecond value, or null. */
export function ledgerTime(iso: string | undefined): number | null {
  if (!iso) return null
  const at = Date.parse(iso)
  return Number.isFinite(at) ? at : null
}

/**
 * Build the axis from the drawn model. Pure: same nodes, same axis.
 *
 * Sorting is total — `(created_at, id)` — so a later poll that carries the same
 * rows produces the same order and the cursor does not jump. Rows that share a
 * second (the common case for a burst of facts) are ordered by id, which is the
 * ledger's own tie-break.
 */
export function buildTimeline(nodes: readonly GraphNode[]): GraphTimeline {
  const timed: { id: string; at: number; iso: string }[] = []
  const untimed: string[] = []
  for (const n of nodes) {
    if (undatedByType(n.type)) continue
    const at = ledgerTime(n.orderTime)
    const iso = (n.orderTime ?? '').trim()
    if (at === null || !iso) {
      untimed.push(n.id)
      continue
    }
    timed.push({ id: n.id, at, iso })
  }
  timed.sort((a, b) => (a.at !== b.at ? a.at - b.at : a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
  const rank = new Map<string, number>()
  for (const [i, row] of timed.entries()) rank.set(row.id, i)
  return { order: timed.map((r) => r.id), times: timed.map((r) => r.iso), rank, untimed }
}

/**
 * Slider position → row index. `fraction` is 0..1 over the axis; the last row is
 * the default ("latest"), and an empty axis has no cursor at all.
 */
export function cursorAt(timeline: GraphTimeline, fraction: number): number | null {
  const last = timeline.order.length - 1
  if (last < 0) return null
  const clamped = Math.min(1, Math.max(0, fraction))
  return Math.round(clamped * last)
}

/** Row index → 0..1 slider position. */
export function cursorFraction(timeline: GraphTimeline, cursor: number): number {
  const last = timeline.order.length - 1
  if (last <= 0) return 1
  return Math.min(1, Math.max(0, cursor / last))
}

/**
 * What the axis reveals at `cursor`: the timed rows up to and including it. The
 * paint pass checks membership by rank, so this is only for counts and tests;
 * nothing here allocates per frame.
 */
export function revealedCount(timeline: GraphTimeline, cursor: number): number {
  if (timeline.order.length === 0) return 0
  return Math.min(timeline.order.length, Math.max(0, cursor + 1))
}

/** The axis is worth showing only when at least two rows can be ordered. */
export function timelineUsable(timeline: GraphTimeline): boolean {
  return timeline.order.length > 1
}

/**
 * The one gate rule the painter and the tests share: an id is drawn when it has
 * no rank (undated, the brief, a boundary leaf) or when its row is at or before
 * the cursor.
 */
export function revealedIn(
  rank: ReadonlyMap<string, number>,
  cursor: number,
  id: string,
): boolean {
  const at = rank.get(id)
  return at === undefined || at <= cursor
}

/**
 * Would this id still be drawn at `cursor`? Undated ids (including the brief and
 * boundary leaves) always pass: the axis only hides rows it can actually order.
 */
export function visibleAt(timeline: GraphTimeline, id: string, cursor: number): boolean {
  return revealedIn(timeline.rank, cursor, id)
}
