// Import-preview reading logic (spec #1 工单8/#9). Pure functions: the dialog
// renders, these decide which group a host is in, what a checkbox does, and
// what the confirm will actually do — so the grouping rules have tests without
// a DOM.

import type { ImportPreview, ImportPreviewItem } from './types.ts'

export type ImportKind = 'assets' | 'traffic'

export interface ImportGroups {
  inFence: ImportPreviewItem[]
  outOfFence: ImportPreviewItem[]
  unjudged: ImportPreviewItem[]
}

/** Three groups, never invented: an unknown classification is a contract
 *  break, not a fourth bucket. It reads as 无法判定 (the answer we do not have)
 *  rather than silently becoming "in range". */
export function groupImportPreview(preview: ImportPreview | undefined): ImportGroups {
  const groups: ImportGroups = { inFence: [], outOfFence: [], unjudged: [] }
  for (const item of preview?.items ?? []) {
    if (item.classification === 'in_fence') groups.inFence.push(item)
    else if (item.classification === 'out_of_fence') groups.outOfFence.push(item)
    else groups.unjudged.push(item)
  }
  return groups
}

/** Only out-of-fence hosts are checkable: the in-fence ones need no write, and
 *  an unjudged one has no fence to join. */
export function isSelectable(item: ImportPreviewItem): boolean {
  return item.classification === 'out_of_fence'
}

export function toggleImportHost(selected: ReadonlySet<string>, host: string, selectable: boolean): Set<string> {
  const next = new Set(selected)
  if (!selectable) return next
  if (next.has(host)) next.delete(host)
  else next.add(host)
  return next
}

/** 全选 toggles the whole out-of-fence group: all checked -> clear them; else
 *  add every one (leaving any selection that is not in the group untouched —
 *  there is none today, but the rule is explicit). */
export function selectAllOutOfFence(groups: ImportGroups, selected: ReadonlySet<string>): Set<string> {
  const hosts = groups.outOfFence.map((item) => item.host)
  const all = hosts.length > 0 && hosts.every((host) => selected.has(host))
  const next = new Set(selected)
  for (const host of hosts) {
    if (all) next.delete(host)
    else next.add(host)
  }
  return next
}

/** The impact sentence's numbers: how many hosts join the fence and how many
 *  records/hosts the batch itself carries. */
export function selectionImpact(preview: ImportPreview | undefined, selected: ReadonlySet<string>) {
  const groups = groupImportPreview(preview)
  const hosts = groups.outOfFence.filter((item) => selected.has(item.host)).map((item) => item.host)
  return {
    fenceHosts: hosts,
    fenceCount: hosts.length,
    records: preview?.records ?? 0,
    totalHosts: preview?.hosts ?? 0,
    allSelected: groups.outOfFence.length > 0 && hosts.length === groups.outOfFence.length,
  }
}

export function importKindLabel(kind: ImportKind): string {
  return kind === 'traffic' ? 'HAR 流量' : '扫描结果'
}

/** What the confirm button refuses before the server sees it. */
export function importConfirmProblem(preview: ImportPreview | undefined, path: string, busy: boolean): string | undefined {
  if (busy) return '正在导入…'
  if (!preview) return '先预览一次，确认这次导入会落什么。'
  if (!path.trim()) return '填写服务器上的文件路径。'
  return undefined
}
