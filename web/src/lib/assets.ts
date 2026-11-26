// The asset page's reading logic (spec #1 工单6/#7). Pure functions only: the
// table and the detail panel render, these decide — so the filters and the
// three-state reading have tests without a DOM.

import type { AssetRow } from './types.ts'

/** The fence chips the page filters by. `unregistered` means the engagement
 *  has no asset record for the row (未登记) — never "out of fence". */
export type FenceFilter = '' | 'in_fence' | 'pending' | 'denied' | 'unregistered'

export function fenceStateOf(row: Pick<AssetRow, 'fence'>): Exclude<FenceFilter, ''> {
  switch (row.fence) {
    case 'in_fence':
    case 'pending':
    case 'denied':
      return row.fence
    default:
      return 'unregistered'
  }
}

export const FENCE_LABELS: Record<Exclude<FenceFilter, ''>, string> = {
  in_fence: '围栏内',
  pending: '待审',
  denied: '已拒',
  unregistered: '未登记',
}

/** Fence chip variant per state: pending is the one that needs the operator. */
export function fenceVariant(fence: string | undefined): 'success' | 'warning' | 'destructive' | 'muted' {
  switch (fence) {
    case 'in_fence':
      return 'success'
    case 'pending':
      return 'warning'
    case 'denied':
      return 'destructive'
    default:
      return 'muted'
  }
}

/** The honest three-state test reading (mirrors the server's derivation). */
export const TESTED_LABELS: Record<string, { label: string; variant: 'muted' | 'secondary' | 'success' }> = {
  untouched: { label: '未触达', variant: 'muted' },
  reached: { label: '已触达', variant: 'secondary' },
  concluded: { label: '有结论', variant: 'success' },
}

export function testedOf(row: Pick<AssetRow, 'tested'>): { label: string; variant: 'muted' | 'secondary' | 'success' } {
  return TESTED_LABELS[row.tested] ?? TESTED_LABELS.untouched!
}

/** "2xx 12 · 4xx 3 · error 1" — a compact shape, buckets only. */
export function statusSummary(statuses: Record<string, number> | undefined): string {
  if (!statuses) return ''
  const order = ['2xx', '3xx', '4xx', '5xx', 'error', 'none']
  const parts: string[] = []
  for (const bucket of order) {
    const n = statuses[bucket]
    if (n) parts.push(`${bucket} ${n}`)
  }
  return parts.join(' · ')
}

export interface AssetTableFilter {
  query: string
  fence: FenceFilter
}

/** Search matches the row key and any observed host; the fence chip narrows
 *  by the row's own standing. */
export function filterAssetRows(rows: AssetRow[], filter: AssetTableFilter): AssetRow[] {
  const needle = filter.query.trim().toLowerCase()
  return rows.filter((row) => {
    if (filter.fence && fenceStateOf(row) !== filter.fence) return false
    if (!needle) return true
    if (row.key.toLowerCase().includes(needle)) return true
    return row.hosts.some((host) => host.toLowerCase().includes(needle))
  })
}

/** The kind label the table shows next to a key: a CIDR range is not a
 *  hostname row and must not read like one. */
export function kindLabel(kind: string): string {
  switch (kind) {
    case 'range':
      return '网段'
    case 'host':
      return 'IP'
    case 'service':
      return '端口'
    default:
      return '域名'
  }
}
