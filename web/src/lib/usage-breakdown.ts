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
