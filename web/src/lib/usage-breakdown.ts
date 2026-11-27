// Project token accounting (GET /api/usage/breakdown) presentation rules.
//
// Every number is model-reported usage. The console renders exactly what the
// server reported: an absent bucket/field is UNKNOWN and prints "—", never a
// measured 0 (fmtInt(undefined) would happily print "0", so presence is checked
// first). A real zero that the server did report stays "0".
//
// The layout mirrors the contract: commander / executor(+review/report) /
// combined, then the judge split into JEV and LLM, then the all-in total.

import { fmtInt } from './format.ts'
import type { UsageBreakdown, UsageBucket } from './types.ts'

export type UsageField = 'total' | 'input' | 'output' | 'runs' | 'calls'

/** A reported finite number, or undefined when the field is absent/malformed. */
export function usageNumber(bucket: UsageBucket | undefined, field: UsageField): number | undefined {
  const value = bucket?.[field]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

/** Format a reported number; an absent one stays "—" (never a fabricated 0). */
export function usageText(value: number | undefined): string {
  return value === undefined ? '—' : fmtInt(value)
}

/** The bucket's total as text. */
export function usageBucketText(bucket: UsageBucket | undefined): string {
  return usageText(usageNumber(bucket, 'total'))
}

/** `runs 3 · 入 1,200 · 出 300` — only the fields that were actually reported. */
export function usageDetailText(bucket: UsageBucket | undefined): string {
  if (!bucket) return ''
  const parts: string[] = []
  const runs = usageNumber(bucket, 'runs')
  if (runs !== undefined) parts.push(`runs ${fmtInt(runs)}`)
  const calls = usageNumber(bucket, 'calls')
  if (calls !== undefined) parts.push(`调用 ${fmtInt(calls)}`)
  const input = usageNumber(bucket, 'input')
  if (input !== undefined) parts.push(`入 ${fmtInt(input)}`)
  const output = usageNumber(bucket, 'output')
  if (output !== undefined) parts.push(`出 ${fmtInt(output)}`)
  return parts.join(' · ')
}

export interface UsagePart {
  key: string
  label: string
  bucket?: UsageBucket
  /** Reported total; absent means the server did not report this bucket. */
  total?: number
  /** Already formatted total ("—" when unknown). */
  text: string
  detail: string
  available: boolean
}

export interface UsageBreakdownView {
  /** 指挥官 / 执行者（含复核·报告）/ 合计（指挥官+执行者） */
  main: UsagePart[]
  /** 裁判：JEV / LLM，kept separate on purpose. */
  judge: UsagePart[]
  judgeTotal: UsagePart
  all: UsagePart
  /** True when at least one total was reported (including a real 0). */
  anyData: boolean
  /** Labels whose total is absent, for the degradation note. */
  missing: string[]
  note: string
  /** `journal=available · ledger=unavailable`, only for scalar source notes. */
  sources: string
}

function part(key: string, label: string, bucket: UsageBucket | undefined): UsagePart {
  const total = usageNumber(bucket, 'total')
  return {
    key,
    label,
    bucket,
    total,
    text: usageText(total),
    detail: usageDetailText(bucket),
    available: total !== undefined,
  }
}

/** Normalise one breakdown response into labelled, pre-formatted rows. */
export function usageBreakdownView(breakdown?: UsageBreakdown | null): UsageBreakdownView {
  const commander = part('commander', '指挥官', breakdown?.commander)
  // The contract names executor_total as executor + review/report; a server
  // that only reports `executor` is still shown, at the same label.
  const executor = part('executor', '执行者（含复核·报告）', breakdown?.executor_total ?? breakdown?.executor)
  const combined = part('combined', '合计（指挥官+执行者）', breakdown?.combined)
  const jev = part('jev', 'JEV', breakdown?.judge?.jev)
  const llm = part('llm', 'LLM', breakdown?.judge?.llm)
  const judgeTotal = part('judge', '裁判合计', breakdown?.judge?.total)
  const all = part('all', '全部（含裁判）', breakdown?.all)
  const parts = [commander, executor, combined, jev, llm, judgeTotal, all]
  const anyData = parts.some((entry) => entry.available)
  const missing = parts.filter((entry) => !entry.available).map((entry) => entry.label)
  let note = ''
  if (!breakdown) {
    note = '还没有读到 Token 分账数据；不估算、不补零。'
  } else if (!anyData) {
    note = '后端返回了空分账：可能还没有模型调用，或 journal/ledger 都没有可用用量。显示 — 表示未上报，不是 0。'
  } else if (missing.length > 0) {
    note = `部分分账未上报：${missing.join('、')}（显示为 —，不补零）。`
  }
  return {
    main: [commander, executor, combined],
    judge: [jev, llm],
    judgeTotal,
    all,
    anyData,
    missing,
    note,
    sources: usageSourcesText(breakdown?.sources),
  }
}

/** The optional `sources` availability notes, scalar entries only. */
export function usageSourcesText(sources?: Record<string, unknown>): string {
  if (!sources || typeof sources !== 'object') return ''
  const parts: string[] = []
  for (const [key, value] of Object.entries(sources)) {
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      parts.push(`${key}=${String(value)}`)
    }
  }
  return parts.join(' · ')
}

/** The one-line summary used when the card is collapsed. */
export function usageSummaryText(view: UsageBreakdownView): string {
  return view.main.map((entry) => `${entry.label} ${entry.text}`).join(' · ')
}

// ---- overview composition ---------------------------------------------------

export interface UsageSegment {
  key: 'commander' | 'executor' | 'judge'
  label: string
  /** Reported total; absent means the server did not report this spender. */
  value?: number
  /** Pre-formatted total ("—" when unknown). */
  text: string
  /** Share of the all-in total, only when every spender reported. */
  pct?: number
  /** Pre-formatted share ("20%", "<1%", "0%", "—"). */
  pctText: string
}

export interface UsageComposition {
  /** 指挥官 / 执行者（含复核·报告）/ 裁判 — the three ways tokens are spent. */
  segments: UsageSegment[]
  /** Denominator for the bar: the reported all-in total, or the sum when all parts are known. */
  total?: number
  totalText: string
  /** Every spender reported a total, so shares are meaningful. */
  shareKnown: boolean
  /** Labels with no reported total (the note names them). */
  unknown: string[]
}

/** A share of the reported total; sub-1% stays visible as "<1%", a real 0 as "0%". */
function usagePct(value: number | undefined, total: number | undefined): { pct?: number; text: string } {
  if (value === undefined || total === undefined) return { text: '—' }
  if (total <= 0) return { pct: 0, text: '0%' }
  const pct = (value / total) * 100
  return { pct, text: pct > 0 && pct < 1 ? '<1%' : `${Math.round(pct)}%` }
}

/**
 * The three spenders as one composition, for the overview bar.
 *
 * Shares are printed only when all three buckets reported: renormalising the
 * known parts would turn 1,000 + 3,000 with an unknown judge into a fake
 * 25% / 75% of a total nobody measured. The bar's denominator is the reported
 * all-in total, or the sum when every part is known.
 */
export function usageComposition(view: UsageBreakdownView): UsageComposition {
  const entries: { key: UsageSegment['key']; label: string; part: UsagePart }[] = [
    { key: 'commander', label: '指挥官', part: view.main[0] },
    { key: 'executor', label: '执行者（含复核·报告）', part: view.main[1] },
    { key: 'judge', label: '裁判', part: view.judgeTotal },
  ]
  const shareKnown = entries.every((entry) => entry.part.available)
  const sum = entries.reduce((acc, entry) => acc + (entry.part.total ?? 0), 0)
  const total = view.all.available ? view.all.total : shareKnown ? sum : undefined
  const segments = entries.map(({ key, label, part }): UsageSegment => {
    if (!shareKnown) {
      return { key, label, value: part.total, text: part.text, pctText: '—' }
    }
    const share = usagePct(part.total, total)
    return { key, label, value: part.total, text: part.text, pct: share.pct, pctText: share.text }
  })
  return {
    segments,
    total,
    totalText: usageText(total),
    shareKnown,
    unknown: entries.filter((entry) => !entry.part.available).map((entry) => entry.label),
  }
}

// ---- overview 24h rhythm ----------------------------------------------------

const HOUR_MS = 3_600_000

const pad2 = (value: number) => String(value).padStart(2, '0')

export interface UsageHourBucket {
  /** Local-hour identity, e.g. 2026-11-27T14 (used as the React key). */
  key: string
  /** Epoch ms of the hour's start. */
  start: number
  /** Local clock label, e.g. "14:00". */
  label: string
  /** Reported tokens; undefined when the hour had runs but none reported usage. */
  tokens?: number
  runs: number
  /** Runs in this hour whose harness never reported usage (excluded from `tokens`). */
  unknownRuns: number
  /** The current, still-running hour. */
  partial: boolean
}

export interface UsageHourlySeries {
  /** Oldest → newest, `hours` buckets; the last one is the in-progress hour. */
  buckets: UsageHourBucket[]
  /** Reported tokens in the window; undefined when runs exist but none reported. */
  total?: number
  totalText: string
  /** The 24 hours before the window, same honesty rule. */
  previous?: number
  /** Signed percent vs `previous`; undefined without a reported, non-zero base. */
  deltaPct?: number
  /** Busiest hour with reported spend, excluding the in-progress hour. */
  peak?: { label: string; tokens: number }
  /** Window runs with no reported usage (their tokens are unknown, not 0). */
  unknownRuns: number
  /** Judge usage lives in journals, not run rows: the series never includes it. */
  excludesJudge: true
}

export interface UsageHourlyOptions {
  hours?: number
  now?: number
}

/**
 * Bucket ledger runs into local hours for the overview sparkline.
 *
 * Runs are bucketed by `started_at`: a long run lands in the hour it began, so
 * the strip answers "when was work launched", not "when did a bill settle".
 * Judge tokens are intentionally outside this series — they are journal rows,
 * not runs — and the UI labels that.
 */
export function usageHourlySeries(
  runs: readonly { started_at?: string; tokens?: number }[],
  options: UsageHourlyOptions = {},
): UsageHourlySeries {
  const hours = Math.max(1, Math.floor(options.hours ?? 24))
  const now = options.now ?? Date.now()
  const currentStart = Math.floor(now / HOUR_MS) * HOUR_MS
  const firstStart = currentStart - (hours - 1) * HOUR_MS
  const previousStart = firstStart - hours * HOUR_MS

  const sums = Array.from({ length: hours }, () => ({ tokens: 0, reported: 0, runs: 0, unknown: 0 }))
  const previous = { tokens: 0, reported: 0, runs: 0, unknown: 0 }

  for (const run of runs) {
    const at = Date.parse(run.started_at ?? '')
    if (!Number.isFinite(at)) continue
    const tokens = typeof run.tokens === 'number' && Number.isFinite(run.tokens) ? run.tokens : undefined
    const index = Math.floor((at - firstStart) / HOUR_MS)
    if (at >= firstStart && at < currentStart + HOUR_MS && index >= 0 && index < hours) {
      const sum = sums[index]
      sum.runs++
      if (tokens === undefined) sum.unknown++
      else {
        sum.tokens += tokens
        sum.reported++
      }
      continue
    }
    if (at >= previousStart && at < firstStart) {
      previous.runs++
      if (tokens === undefined) previous.unknown++
      else {
        previous.tokens += tokens
        previous.reported++
      }
    }
  }

  const buckets: UsageHourBucket[] = sums.map((sum, index) => {
    const start = firstStart + index * HOUR_MS
    const date = new Date(start)
    const label = `${pad2(date.getHours())}:00`
    return {
      key: `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}T${pad2(date.getHours())}`,
      start,
      label,
      // No runs is a measured zero; runs with no reported usage stay unknown.
      tokens: sum.reported > 0 ? sum.tokens : sum.runs > 0 ? undefined : 0,
      runs: sum.runs,
      unknownRuns: sum.unknown,
      partial: start === currentStart,
    }
  })

  const windowRuns = sums.reduce((acc, sum) => acc + sum.runs, 0)
  const windowReported = sums.reduce((acc, sum) => acc + sum.reported, 0)
  const total = windowRuns === 0 ? 0 : windowReported > 0 ? sums.reduce((acc, sum) => acc + sum.tokens, 0) : undefined
  const previousTotal =
    previous.runs === 0 ? 0 : previous.reported > 0 ? previous.tokens : undefined
  const deltaPct =
    total !== undefined && previousTotal !== undefined && previousTotal > 0
      ? Math.round(((total - previousTotal) / previousTotal) * 100)
      : undefined

  let peak: UsageHourlySeries['peak']
  for (const bucket of buckets) {
    if (bucket.partial || bucket.tokens === undefined || bucket.tokens <= 0) continue
    if (!peak || bucket.tokens > peak.tokens) peak = { label: bucket.label, tokens: bucket.tokens }
  }

  return {
    buckets,
    total,
    totalText: usageText(total),
    previous: previousTotal,
    deltaPct,
    peak,
    unknownRuns: sums.reduce((acc, sum) => acc + sum.unknown, 0),
    excludesJudge: true,
  }
}

// ---- overview coverage ------------------------------------------------------

export interface UsageCoverage {
  /** Runs in the project whose harness never reported usage. */
  unknownRuns: number
  /** Model calls inside reported runs that returned without usage. */
  missingCalls: number
  /** The backend's own reasons (empty means every input was readable). */
  unavailable: string[]
  /** One quiet line for the panel; empty when everything was reported. */
  text: string
}

function reportedCount(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

/**
 * What the numbers do not cover. Counts come from the all-in bucket (the widest
 * one), reasons from the contract's `unavailable` list — the panel prints them
 * instead of quietly presenting a partial total as complete.
 */
export function usageCoverage(breakdown?: UsageBreakdown | null): UsageCoverage {
  const all = breakdown?.all
  const unknownRuns = reportedCount(all?.unknown_usage_runs)
  const missingCalls = reportedCount(all?.missing_usage)
  const unavailable = Array.isArray(breakdown?.unavailable) ? breakdown!.unavailable!.filter(Boolean) : []
  const parts: string[] = []
  if (unknownRuns > 0) parts.push(`${fmtInt(unknownRuns)} 个 run 未上报用量`)
  if (missingCalls > 0) parts.push(`${fmtInt(missingCalls)} 次调用缺用量`)
  return { unknownRuns, missingCalls, unavailable, text: parts.join(' · ') }
}
