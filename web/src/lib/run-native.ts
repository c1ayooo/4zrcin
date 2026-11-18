// Run-scoped native-session reads: the host history page and command menu.
//
// The host's event payload is proto JSON the console does not own, so it stays
// raw; these helpers only pull a stable summary out of it when the fields are
// recognisable, and otherwise show the raw document honestly. The error mapping
// keeps "no native session" (409) and "no reachable host" (503) as explanations
// rather than red failures: an old local audit run legitimately has neither.

import type { NativeSessionEvent, RunCommand, RunHistoryPage } from './types.ts'

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : null
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function clip(value: string, limit: number): string {
  return value.length > limit ? `${value.slice(0, limit)}…` : value
}

export interface HistoryEventSummary {
  kind: string
  state?: string
  at?: string
  text: string
}

/**
 * A compact row for one canonical host event. The schema belongs to the
 * harness, so unknown shapes fall back to clipped JSON instead of pretending
 * the event is empty.
 */
export function historyEventSummary(event: unknown): HistoryEventSummary {
  const record = asRecord(event)
  if (!record) {
    return { kind: 'event', text: clip(text(event) || JSON.stringify(event ?? null), 240) }
  }
  const payload = asRecord(record.payload) ?? asRecord(record.event) ?? record
  const kind =
    text(record.kind) || text(record.type) || text(payload.kind) || text(payload.type) || 'event'
  const state = text(record.state) || text(payload.state)
  const at =
    text(record.at) || text(record.time) || text(record.timestamp) || text(record.emitted_at) ||
    text(payload.at) || text(payload.time)
  let body =
    text(record.text) || text(record.content) || text(payload.text) || text(payload.content)
  const message = asRecord(payload.message)
  if (!body && message) body = text(message.text)
  if (!body) body = JSON.stringify(event)
  const summary: HistoryEventSummary = { kind, text: clip(body, 240) }
  if (state) summary.state = state
  if (at) summary.at = at
  return summary
}

/**
 * Append one page to the already-loaded canonical events. Cursors are the
 * host's stable ids: a repeated cursor (a retried page) is ignored instead of
 * duplicated, so "加载更多" can never grow the list twice for one page.
 */
export function appendRunHistory(previous: NativeSessionEvent[], page: RunHistoryPage): NativeSessionEvent[] {
  const seen = new Set(previous.map((item) => item.cursor))
  const out = [...previous]
  for (const item of page.events ?? []) {
    if (!item || !item.cursor || seen.has(item.cursor)) continue
    seen.add(item.cursor)
    out.push(item)
  }
  return out
}

/** The cursor to continue from ('' = no more pages). */
export function nextRunHistoryCursor(page: RunHistoryPage | undefined): string {
  if (!page) return ''
  if (page.next_cursor) return page.next_cursor
  // An older host may omit has_more; a missing cursor is the honest end.
  return page.has_more ? page.next_cursor ?? '' : ''
}

export function nativeSessionErrorText(error: unknown): {
  tone: 'info' | 'warning' | 'destructive'
  text: string
} {
  const record = asRecord(error)
  const status = typeof record?.status === 'number' ? record.status : 0
  const code = text(record?.code)
  const message = text(record?.message) || String(error)
  if (status === 409 || code === 'no_native_session') {
    return { tone: 'info', text: `这个运行没有原生 AOP 会话（旧运行或本地 audit 节点），没有宿主侧数据可读。${message ? ` ${message}` : ''}` }
  }
  if (status === 503 || code === 'host_unavailable') {
    return { tone: 'info', text: `运行所在节点没有可拨号的 AOP 宿主，无法读取宿主侧会话数据。${message ? ` ${message}` : ''}` }
  }
  if (status === 502 || code === 'host_read_failed') {
    return { tone: 'warning', text: `读取宿主会话失败（宿主返回错误）：${message}` }
  }
  return { tone: 'warning', text: `读取宿主会话失败：${message}` }
}

/** Filter command-menu rows by name/alias/usage/description (case-insensitive). */
export function commandMatches(command: RunCommand, query: string): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  return [command.name, ...(command.aliases ?? []), command.usage ?? '', command.description ?? '']
    .join(' ')
    .toLowerCase()
    .includes(needle)
}

/** `aliases: a, b · 用法` — the second line of a command row. */
export function commandDetailText(command: RunCommand): string {
  const parts: string[] = []
  if (command.aliases?.length) parts.push(`别名 ${command.aliases.join(' / ')}`)
  if (command.usage) parts.push(command.usage)
  return parts.join(' · ')
}
