import { focusStatusLabel } from './focus.ts'
import type { Fact, FrontierItem } from './types.ts'

/**
 * One direction's own detail is read from three places that must not be merged
 * into a fourth vocabulary:
 *
 *  - the intent row (status / wait / park / hold) — 现状 and 等待原因;
 *  - its runs (`purpose == ""`) — 历次运行;
 *  - the facts it declared (`GET /api/intents/{id}`) — 关联证据.
 *
 * The helpers here only count and classify what the server returned. They never
 * decide that a fact is proven: status and evidence count are shown as stored.
 * A premise the page did not receive is reported as missing, not as verified.
 */

/** Which gate a fact sits on for this direction. */
export type IntentFactRole = 'premise' | 'declared'

export interface IntentFactRow {
  id: string
  kind: string
  title: string
  /** 未复核 / 已复核 / 已否定 / 已撤回 — the focus panel's labels, not a second table. */
  status: string
  /** Raw ledger status, for the tooltip and for filtering. */
  rawStatus: string
  version: number
  evidence: number
  role: IntentFactRole
  createdAt: string
}

export interface IntentFactsSummary {
  rows: IntentFactRow[]
  /** `facts_total` from the response; equals `listed` when the server did not say. */
  total: number
  listed: number
  verified: number
  unverified: number
  /** rejected + withdrawn: the direction cannot stand on these either. */
  closed: number
  /** Sum of evidence ids across the listed rows (not a project-wide count). */
  evidence: number
  premises: number
  /** Hard premises (from_fact_ids) this page did not receive a row for. */
  missingPremises: string[]
  /** The server holds more rows than this page returned. */
  truncated: boolean
}

/**
 * Classify the facts a direction declared. `premiseIDs` are the direction's hard
 * premises (from_fact_ids): a premise stays a premise even when the direction
 * also declared it, because that is the gate the dispatch reads.
 */
export function summarizeIntentFacts(
  facts: Fact[],
  opts: { premiseIDs?: string[]; total?: number } = {},
): IntentFactsSummary {
  const premiseIDs = unique(opts.premiseIDs)
  const premise = new Set(premiseIDs)
  const rows: IntentFactRow[] = facts.map((fact) => ({
    id: fact.id,
    kind: fact.kind || 'fact',
    title: fact.title || fact.summary || fact.id,
    status: focusStatusLabel(fact.status),
    rawStatus: fact.status,
    version: fact.claim_version ?? 0,
    evidence: fact.evidence_ids?.length ?? 0,
    role: premise.has(fact.id) ? 'premise' : 'declared',
    createdAt: fact.created_at,
  }))
  const seen = new Set(rows.map((row) => row.id))
  return {
    rows,
    total: Math.max(opts.total ?? rows.length, rows.length),
    listed: rows.length,
    verified: rows.filter((row) => row.rawStatus === 'verified').length,
    unverified: rows.filter((row) => row.rawStatus === 'unverified').length,
    closed: rows.filter((row) => row.rawStatus === 'rejected' || row.rawStatus === 'withdrawn').length,
    evidence: rows.reduce((sum, row) => sum + row.evidence, 0),
    premises: rows.filter((row) => row.role === 'premise').length,
    missingPremises: premiseIDs.filter((id) => id && !seen.has(id)),
    truncated: (opts.total ?? rows.length) > rows.length,
  }
}

export interface PremiseBlocker {
  id: string
  /** Why the premise is unusable — the board appends it as 不存在/已撤回/已否定. */
  reason: string
}

export interface PremiseBlockers {
  /** Premises whose fact is missing/withdrawn/rejected: dispatch will be refused. */
  blocked: PremiseBlocker[]
  /** Premises whose assertion version moved after this direction was written. */
  changed: string[]
}

/**
 * The board's frontier row carries blocked premises as `"<id>（<reason>）"` (see
 * board.go: it appends 不存在 / 已撤回 / 已否定), so the detail must split the two:
 * the id is what a link can open, the reason is what the operator reads. A fact
 * that does not exist cannot be opened and stays plain text.
 */
export function premiseBlockers(frontier?: FrontierItem): PremiseBlockers {
  const blocked: PremiseBlocker[] = []
  for (const entry of frontier?.blocked_on ?? []) {
    const parsed = parseBlocker(entry)
    if (parsed && !blocked.some((row) => row.id === parsed.id)) blocked.push(parsed)
  }
  return { blocked, changed: unique(frontier?.premise_changed) }
}

function parseBlocker(entry?: string): PremiseBlocker | undefined {
  if (!entry) return undefined
  const match = /^(.*?)（([^（）]*)）$/.exec(entry)
  if (!match) return { id: entry, reason: '' }
  return { id: match[1].trim(), reason: match[2].trim() }
}

function unique(ids?: string[]): string[] {
  const out: string[] = []
  for (const id of ids ?? []) if (id && !out.includes(id)) out.push(id)
  return out
}
