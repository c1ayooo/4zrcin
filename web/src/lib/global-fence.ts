// The instance fence page's reading logic (spec #1 工单3/#9). Pure functions:
// the page renders, these decide list transformations, validation and the
// labels the audit table shows — so the rules have tests without a DOM.

import type { FenceHit, GlobalFence, GlobalFenceCommand, GlobalFenceDomain } from './types.ts'

export const COMMAND_ACTION_LABELS: Record<string, string> = {
  deny: '拒绝',
  review: '待审',
}

export const HIT_SOURCE_LABELS: Record<string, string> = {
  global: '全局',
  rule: '项目规则',
}

export const HIT_KIND_LABELS: Record<string, string> = {
  command: '命令',
  domain: '域名',
}

export const HIT_ACTION_LABELS: Record<string, string> = {
  block: '拒绝',
  review: '待审',
}

export function emptyGlobalFence(): GlobalFence {
  return { enabled_commands: false, commands: [], enabled_domains: false, domains: [] }
}

/** The wire omits empty arrays and false switches (Go's omitempty), so a stored
 *  empty fence arrives as `{}`. Fill the two arrays once at the edge; every
 *  component below can then read them without guards. */
export function normalizeClientFence(raw: Partial<GlobalFence> | undefined): GlobalFence {
  return {
    enabled_commands: raw?.enabled_commands ?? false,
    commands: raw?.commands ?? [],
    enabled_domains: raw?.enabled_domains ?? false,
    domains: raw?.domains ?? [],
  }
}

/** A list is "armed" only when it is enabled AND holds rows: a switch on an
 *  empty list changes nothing, and the page should not read it as coverage. */
export function fenceListArmed(enabled: boolean, rows: unknown[]): boolean {
  return enabled && rows.length > 0
}

export function fenceSummary(fence: GlobalFence) {
  const denyCommands = fence.commands.filter((row) => row.action !== 'review').length
  return {
    commands: fence.commands.length,
    domains: fence.domains.length,
    denyCommands,
    reviewCommands: fence.commands.length - denyCommands,
    commandsArmed: fenceListArmed(fence.enabled_commands, fence.commands),
    domainsArmed: fenceListArmed(fence.enabled_domains, fence.domains),
  }
}

/** Enabling a list that already holds rows is the dangerous action: from the
 *  next turn every node refuses (or asks) on a match. Disabling never needs a
 *  confirmation — it is the safe direction and keeps the rows. */
export function needsEnableConfirm(rows: unknown[], enabledAfter: boolean): boolean {
  return enabledAfter && rows.length > 0
}

function newID(): string {
  // A client-side id is a placeholder; the server assigns the stable one. It
  // exists only so React keys and inline edits have an identity before save.
  return `draft-${Math.random().toString(36).slice(2, 10)}`
}

/** The wire shape: a client-side placeholder id is dropped so the server can
 *  assign the stable one. Saved rows keep their id verbatim. */
export function forSave(fence: GlobalFence): GlobalFence {
  const clean = (id: string) => (id.startsWith('draft-') ? '' : id)
  return {
    ...fence,
    commands: fence.commands.map((row) => ({ ...row, id: clean(row.id) })),
    domains: fence.domains.map((row) => ({ ...row, id: clean(row.id) })),
  }
}

export function withCommandAdded(fence: GlobalFence, row: Omit<GlobalFenceCommand, 'id'>): GlobalFence {
  return { ...fence, commands: [...fence.commands, { ...row, id: newID() }] }
}

export function withCommandUpdated(fence: GlobalFence, id: string, patch: Partial<GlobalFenceCommand>): GlobalFence {
  return {
    ...fence,
    commands: fence.commands.map((row) => (row.id === id ? { ...row, ...patch, id: row.id } : row)),
  }
}

export function withCommandRemoved(fence: GlobalFence, id: string): GlobalFence {
  return { ...fence, commands: fence.commands.filter((row) => row.id !== id) }
}

export function withDomainAdded(fence: GlobalFence, row: GlobalFenceDomain): GlobalFence {
  return { ...fence, domains: [...fence.domains, { host: row.host, id: newID() }] }
}

export function withDomainUpdated(fence: GlobalFence, id: string, patch: Partial<GlobalFenceDomain>): GlobalFence {
  return {
    ...fence,
    domains: fence.domains.map((row) => (row.id === id ? { ...row, ...patch, id: row.id } : row)),
  }
}

export function withDomainRemoved(fence: GlobalFence, id: string): GlobalFence {
  return { ...fence, domains: fence.domains.filter((row) => row.id !== id) }
}

/** Client-side pre-checks only. The server owns the real validation (Go's
 *  regexp is RE2, not this one) — these stop an obviously broken save early,
 *  and the server's refusal is what the page shows otherwise. */
export const MAX_COMMAND_PATTERN = 200
export const MAX_COMMAND_DESC = 200
export const MAX_DOMAIN_HOST = 253

export function commandProblem(pattern: string, desc: string): string | undefined {
  const body = pattern.trim()
  if (!body) return '写一个匹配模式再保存。'
  if (body.length > MAX_COMMAND_PATTERN) return `模式最多 ${MAX_COMMAND_PATTERN} 字节。`
  if (desc.trim().length > MAX_COMMAND_DESC) return `说明最多 ${MAX_COMMAND_DESC} 字节。`
  return undefined
}

export function domainProblem(host: string): string | undefined {
  const body = host.trim().toLowerCase()
  if (!body) return '写一个域名或网段再保存。'
  if (body.length > MAX_DOMAIN_HOST) return `域名最多 ${MAX_DOMAIN_HOST} 字节。`
  if (body.includes('://')) return '只写域名、IP 或 CIDR：不要带协议。'
  if (/[?#@\s]/.test(body)) return '只写域名、IP 或 CIDR：不要路径、参数或空白。'
  if (body.includes('/')) {
    // A CIDR row: the slash is the range separator, not a path. The server
    // parses it with net.ParseCIDR; this only stops an obvious typo.
    if (!/^[0-9a-f:.]+\/\d{1,3}$/i.test(body)) return 'CIDR 写法应为 10.0.0.0/8 或 fd00::/8。'
    return undefined
  }
  if (body.includes(':')) return '只写域名、IP 或 CIDR：端口不在规则里匹配。'
  return undefined
}

export function hitSourceLabel(source: string): string {
  return HIT_SOURCE_LABELS[source] ?? '未记录'
}

export function hitKindLabel(kind: string): string {
  return HIT_KIND_LABELS[kind] ?? kind
}

/** The action column: a review row says 待审 until its state settles. */
export function hitActionLabel(hit: Pick<FenceHit, 'action' | 'state'>): string {
  if (hit.action === 'review') {
    if (hit.state === 'approved') return '已批准'
    if (hit.state === 'rejected') return '已拒绝'
    if (hit.state === 'expired') return '已过期'
    return '待审'
  }
  return HIT_ACTION_LABELS[hit.action] ?? '拒绝'
}

/** What the rule column shows: the row's own text, else the id. */
export function hitRuleText(hit: Pick<FenceHit, 'rule_text' | 'rule_id'>): string {
  return hit.rule_text && hit.rule_text.trim() ? hit.rule_text : hit.rule_id
}

export function hitCounts(hits: FenceHit[]) {
  let global = 0
  let rule = 0
  let block = 0
  let review = 0
  for (const hit of hits) {
    if (hit.source === 'global') global++
    else if (hit.source === 'rule') rule++
    if (hit.action === 'block') block++
    else if (hit.action === 'review') review++
  }
  return { global, rule, block, review }
}
