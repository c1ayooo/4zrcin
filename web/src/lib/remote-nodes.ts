// Presentation for the nodes page's tunnel rows (docs/15 §10.3, §10.6): the
// server owns the lifecycle state, this module only turns it into the one
// quiet status line a row shows, plus the few formatters the rows share.
import type { EnrollmentView, NodeInfo, RemoteNode, RemoteNodeInfo } from './types.ts'

export type RemoteStatusKind = 'online' | 'reconnecting' | 'offline' | 'destroying' | 'destroyed' | 'unknown'

export interface RemoteStatus {
  kind: RemoteStatusKind
  label: string
}

/**
 * remoteStatus reads the server-derived state. A tunnel that is up is online;
 * a node the server still reports online while its tunnel is down is
 * reconnecting; everything else is offline. Destroying and destroyed are
 * lifecycle facts and win over connectivity.
 */
export function remoteStatus(remote?: RemoteNodeInfo): RemoteStatus {
  if (!remote) return { kind: 'unknown', label: '状态未知' }
  if (remote.state === 'destroying') return { kind: 'destroying', label: '等待卸载' }
  if (remote.state === 'destroyed') return { kind: 'destroyed', label: '已销毁' }
  if (remote.tunnel_up) return { kind: 'online', label: '隧道已连接' }
  if (remote.state === 'online') return { kind: 'reconnecting', label: '隧道断开 · 重连中' }
  return { kind: 'offline', label: '离线' }
}

export function deployLabel(deploy?: string): string {
  switch (deploy) {
    case 'command':
      return '命令部署'
    case 'ssh':
      return 'SSH 部署'
    default:
      return ''
  }
}

/** A short recipe qualifier; full is the default and stays silent. */
export function recipeLabel(recipe?: string): string {
  if (recipe === 'capture') return 'capture'
  return 'full'
}

/** formatBytes is the mono counter suffix of a node row. */
export function formatBytes(n: number | undefined): string {
  const v = n ?? 0
  if (v < 1024) return `${v} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = v / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

/** formatSince is a coarse connection age ("42m", "3h"); empty when unknown. */
export function formatSince(since: string | undefined, now = Date.now()): string {
  if (!since) return ''
  const started = Date.parse(since)
  if (Number.isNaN(started) || started > now) return ''
  const minutes = Math.floor((now - started) / 60000)
  if (minutes < 1) return '<1m'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

/** enrollmentRemaining is the "剩余 47 分钟" of a pending invitation. */
export function enrollmentRemaining(expiresAt: string, now = Date.now()): string {
  const end = Date.parse(expiresAt)
  if (Number.isNaN(end)) return ''
  const left = end - now
  if (left <= 0) return '已过期'
  const minutes = Math.ceil(left / 60000)
  if (minutes < 60) return `剩余 ${minutes} 分钟`
  return `剩余 ${Math.round(minutes / 60)} 小时`
}

/** remoteSessions indexes /api/remote-nodes rows by the id the node list uses. */
export function remoteSessions(nodes: RemoteNode[] | undefined): Map<string, RemoteNode> {
  const out = new Map<string, RemoteNode>()
  for (const node of nodes ?? []) {
    out.set(node.id, node)
  }
  return out
}

/**
 * sessionOf finds a discovered node's runtime row: the node list names the
 * agent scan_<id>, the remote listing uses the bare node id.
 */
export function sessionOf(sessions: Map<string, RemoteNode>, node: NodeInfo): RemoteNode | undefined {
  return sessions.get(node.id) ?? sessions.get(node.id.replace(/^scan_/, ''))
}

export interface NodeGroups {
  remote: NodeInfo[]
  direct: NodeInfo[]
  local: NodeInfo[]
}

/** groupNodes splits the node list by management boundary (docs/15 §10.3). */
export function groupNodes(nodes: NodeInfo[]): NodeGroups {
  const groups: NodeGroups = { remote: [], direct: [], local: [] }
  for (const node of nodes) {
    if (node.remote) {
      groups.remote.push(node)
      continue
    }
    if (node.local || node.id === 'local') {
      groups.local.push(node)
      continue
    }
    groups.direct.push(node)
  }
  // Online first, then by name: the row the operator reaches for is on top.
  const rank = (n: NodeInfo) => (remoteStatus(n.remote).kind === 'online' ? 0 : 1)
  groups.remote.sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id))
  return groups
}

/** pendingEnrollments are the invitations that have not joined yet. */
export function pendingEnrollments(enrollments: EnrollmentView[] | undefined): EnrollmentView[] {
  return (enrollments ?? []).filter((e) => e.state === 'pending')
}
