// Tool leaderboard arithmetic and ordering.
//
// The console receives per-tool totals from /api/tools: calls, the number of run
// logs the tool appears in, completed results, failures and the summed duration
// of those results. It does not receive a per-call histogram or failure texts, so
// this module can only answer "how often, how often it failed, how long on
// average, how recently" — never a distribution or a failure reason. Keeping the
// arithmetic here makes those limits testable instead of implied.

export interface ToolStatRow {
  tool: string
  local?: boolean
  calls: number
  /** Run logs that contain this tool — a count of logs, not of directions. */
  runs: number
  completed?: number
  failures?: number
  /** Summed duration of completed results, in milliseconds. */
  duration_ms?: number
  /** Top failure buckets from the server (already normalised and capped). */
  error_kinds?: { label: string; count: number }[]
  last_at?: string
}

/** What the list is ordered by. Calls first: it answers "what is used most". */
export type ToolSort = 'calls' | 'failures' | 'duration' | 'recent'

export interface ToolSortOption {
  id: ToolSort
  label: string
}

export const TOOL_SORTS: readonly ToolSortOption[] = [
  { id: 'calls', label: '调用次数' },
  { id: 'failures', label: '失败率' },
  { id: 'duration', label: '平均耗时' },
  { id: 'recent', label: '最近调用' },
]

/** failures / completed, or null while nothing has completed. */
export function failureRate(row: ToolStatRow): number | null {
  const done = row.completed ?? 0
  if (done <= 0) return null
  return (row.failures ?? 0) / done
}

/** Summed duration / completed results, or null when there is no duration. */
export function averageMs(row: ToolStatRow): number | null {
  const done = row.completed ?? 0
  const total = row.duration_ms ?? 0
  if (done <= 0 || total <= 0) return null
  return total / done
}

/** Percent with one decimal below 10%: 3.4% must not read as 3%. */
export function formatRate(rate: number | null): string {
  if (rate === null) return '—'
  const pct = rate * 100
  return `${pct >= 10 ? Math.round(pct) : pct.toFixed(1)}%`
}

/** Milliseconds as a compact human duration; unknown stays unknown. */
export function formatMs(ms: number | null): string {
  if (ms === null) return '—'
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)}s`
  const minutes = Math.floor(ms / 60_000)
  const seconds = Math.round((ms % 60_000) / 1000)
  return seconds ? `${minutes}m${String(seconds).padStart(2, '0')}s` : `${minutes}m`
}

/** Parse a last-call timestamp; unparsable sorts last, never first. */
function lastAt(row: ToolStatRow): number {
  if (!row.last_at) return Number.NEGATIVE_INFINITY
  const at = Date.parse(row.last_at)
  return Number.isFinite(at) ? at : Number.NEGATIVE_INFINITY
}

/**
 * Order the rows for display. Every mode is total and replay-stable: ties fall
 * back to calls, then to the tool name, so a poll that changes nothing cannot
 * reshuffle the list under the pointer.
 *
 * Rows with an unknown rate or duration sort last in that mode — an unknown is
 * not a zero, and putting it first would read as "worst".
 */
export function sortToolRows(rows: readonly ToolStatRow[], sort: ToolSort): ToolStatRow[] {
  const byName = (a: ToolStatRow, b: ToolStatRow) => (a.tool < b.tool ? -1 : a.tool > b.tool ? 1 : 0)
  const tie = (a: ToolStatRow, b: ToolStatRow) => {
    if (a.calls !== b.calls) return b.calls - a.calls
    return byName(a, b)
  }
  return [...rows].sort((a, b) => {
    switch (sort) {
      case 'failures': {
        const ra = failureRate(a)
        const rb = failureRate(b)
        if (ra === null && rb === null) return tie(a, b)
        if (ra === null) return 1
        if (rb === null) return -1
        if (ra !== rb) return rb - ra
        return tie(a, b)
      }
      case 'duration': {
        const da = averageMs(a)
        const db = averageMs(b)
        if (da === null && db === null) return tie(a, b)
        if (da === null) return 1
        if (db === null) return -1
        if (da !== db) return db - da
        return tie(a, b)
      }
      case 'recent': {
        const ta = lastAt(a)
        const tb = lastAt(b)
        if (ta !== tb) return tb - ta
        return tie(a, b)
      }
      default:
        return tie(a, b)
    }
  })
}

/** The list collapses past this many rows; the count stays visible. */
export const TOOL_ROWS_VISIBLE = 10
