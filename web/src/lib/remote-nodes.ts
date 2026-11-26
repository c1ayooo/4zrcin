// Presentation for the nodes page's tunnel rows (docs/14 §9, §10.6): the
// server owns the lifecycle state, this module only turns it into the one
// quiet status line a row shows, plus the few formatters the rows share.
import type { EnrollmentView, NodeHealth, NodeInfo, NodeResources, RemoteNode, RemoteNodeInfo } from './types.ts'

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

/** groupNodes splits the node list by management boundary (docs/14 §9). */
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

// ---- 节点卡片（docs/14 §7.3）：纯函数把节点/心跳变成四张卡片的行列 ----

export interface CardRow {
  label: string
  value: string
}

/** osArch renders "linux/amd64"; empty when neither is known. */
function osArch(os?: string, arch?: string): string {
  if (!os && !arch) return ''
  return [os, arch].filter(Boolean).join('/')
}

export type HealthTone = 'success' | 'warning' | 'destructive' | 'muted'

/** healthTone maps the derived state to the semantic color the card uses. */
export function healthTone(state: NodeHealth['state']): HealthTone {
  if (state === 'healthy') return 'success'
  if (state === 'degraded') return 'warning'
  if (state === 'abnormal') return 'destructive'
  return 'muted'
}

/** healthLabel keeps the card quiet: one word, no punctuation. */
export function healthLabel(state: NodeHealth['state']): string {
  if (state === 'healthy') return '健康'
  if (state === 'degraded') return '降级'
  if (state === 'abnormal') return '异常'
  return '未知'
}

/** formatUptime renders seconds as "3h 12m" / "8m"; empty when unknown. */
export function formatUptime(seconds: number | undefined): string {
  if (!seconds || seconds <= 0) return ''
  const m = Math.floor(seconds / 60)
  if (m < 1) return '<1m'
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

/** pct renders one resource counter as a percentage row value. */
function pct(used: number | undefined, total: number | undefined): string {
  if (!total) return '不支持'
  const v = ((used ?? 0) / total) * 100
  return `${v < 10 ? v.toFixed(1) : Math.round(v)}%`
}

/**
 * nodeResourceRows builds the 资源 card from one sample. `scanLabel` names the
 * scan process row (empty hides it). A missing counter reads 不支持, not 0 —
 * the launcher could not measure it on that kernel.
 */
export function nodeResourceRows(res: NodeResources | undefined, scanLabel = 'scan 内存'): CardRow[] {
  if (!res) return [{ label: '资源采样', value: '暂无（等待首次心跳）' }]
  const rows: CardRow[] = [
    { label: 'CPU', value: res.cpu_percent != null ? `${res.cpu_percent < 10 ? res.cpu_percent.toFixed(1) : Math.round(res.cpu_percent)}%` : '不支持' },
    { label: '内存', value: res.mem_total ? `${formatBytes(res.mem_used)} / ${formatBytes(res.mem_total)}` : '不支持' },
    { label: '磁盘剩余', value: res.disk_total ? `${formatBytes(res.disk_free)} / ${formatBytes(res.disk_total)}` : '不支持' },
  ]
  if (res.scan_rss) rows.push({ label: scanLabel, value: formatBytes(res.scan_rss) })
  return rows
}

/**
 * nodeCardData assembles the four cards for one row: remote rows read the
 * heartbeat row (session), the local row reads the control plane's own sample.
 */
export function nodeCardData(
  node: NodeInfo,
  session: RemoteNode | undefined,
  now = Date.now(),
): { identity: CardRow[]; runtime: CardRow[]; resources: CardRow[]; health: { state: NodeHealth['state']; reasons: string[] } } {
  const isLocal = Boolean(node.local)
  const res = session?.resources ?? node.resources
  const identity: CardRow[] = isLocal
    ? [
        { label: '节点', value: node.id },
        { label: '形态', value: '本机（控制面自带）' },
        { label: '端点', value: node.endpoint || '—' },
      ]
    : [
        { label: '节点', value: session?.name || node.name || node.id },
        { label: '部署', value: deployLabel(session?.deploy) || '命令部署' },
        { label: '配方', value: recipeLabel(session?.recipe) },
        { label: '版本', value: session?.launcher_version || 'dev' },
        { label: '主机', value: [session?.hostname || node.name, osArch(session?.os, session?.arch)].filter(Boolean).join(' · ') || '—' },
      ]
  const runtime: CardRow[] = isLocal
    ? [
        { label: 'Hub', value: node.host?.endpoint || node.endpoint || '—' },
        { label: 'scan 重启', value: String(node.host?.restarts ?? 0) },
        { label: '状态', value: node.host?.unhealthy ? '不健康' : node.host?.ready ? '就绪' : node.host?.last_error || '启动中' },
      ]
    : [
        { label: '隧道', value: session?.tunnel?.up ? '已连接' : '断开' },
        { label: '流量', value: session?.tunnel?.up ? `↑${formatBytes(session.tunnel.bytes_in)} ↓${formatBytes(session.tunnel.bytes_out)}` : '—' },
        { label: '连接时长', value: formatSince(session?.tunnel?.since, now) || '—' },
        { label: 'scan 重启', value: String(session?.scan_restarts ?? 0) },
        { label: '运行时长', value: formatUptime(session?.uptime_seconds) },
        { label: '最近心跳', value: formatSince(session?.last_seen_at, now) || '—' },
      ]
  const health = {
    state: (session?.health?.state ?? node.health?.state ?? '') as NodeHealth['state'],
    reasons: session?.health?.reasons ?? node.health?.reasons ?? [],
  }
  return { identity, runtime, resources: nodeResourceRows(res), health }
}
