// The nodes page's presentation rules (docs/14 §9, §10.6): the server owns
// the lifecycle state, and these helpers turn it into the one quiet status
// line a row shows plus the shared formatters.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  deployLabel,
  enrollmentRemaining,
  formatBytes,
  formatSince,
  groupNodes,
  pendingEnrollments,
  recipeLabel,
  remoteSessions,
  remoteStatus,
  sessionOf,
} from '../src/lib/remote-nodes.ts'
import type { NodeInfo, RemoteNode, RemoteNodeInfo } from '../src/lib/types.ts'

const remote = (over: Partial<RemoteNodeInfo> = {}): RemoteNodeInfo => ({
  deploy: 'command',
  recipe: 'full',
  state: 'online',
  tunnel_up: true,
  dispatch_ready: false,
  dispatch_blocked_reason: '文件合同上线前',
  ...over,
})

test('tunnel state wins over the derived lifecycle, destroying wins over both', () => {
  assert.deepEqual(remoteStatus(remote()), { kind: 'online', label: '隧道已连接' })
  // The server still says online while the tunnel is gone: reconnecting.
  assert.equal(remoteStatus(remote({ tunnel_up: false })).kind, 'reconnecting')
  assert.deepEqual(remoteStatus(remote({ tunnel_up: false, state: 'offline' })), {
    kind: 'offline',
    label: '离线',
  })
  assert.deepEqual(remoteStatus(remote({ state: 'destroying' })), { kind: 'destroying', label: '等待卸载' })
  assert.deepEqual(remoteStatus(remote({ state: 'destroyed', tunnel_up: false })), {
    kind: 'destroyed',
    label: '已销毁',
  })
  assert.deepEqual(remoteStatus(undefined), { kind: 'unknown', label: '状态未知' })
})

test('labels and formatters stay quiet and stable', () => {
  assert.equal(deployLabel('command'), '命令部署')
  assert.equal(deployLabel('ssh'), 'SSH 部署')
  assert.equal(deployLabel(undefined), '')
  assert.equal(recipeLabel('capture'), 'capture')
  assert.equal(recipeLabel('full'), 'full')

  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(900), '900 B')
  assert.equal(formatBytes(12 * 1024 * 1024), '12 MB')
  assert.equal(formatBytes(1536), '1.5 KB')

  const now = Date.parse('2026-11-26T12:00:00Z')
  assert.equal(formatSince('2026-11-26T11:18:00Z', now), '42m')
  assert.equal(formatSince('2026-11-26T09:00:00Z', now), '3h')
  assert.equal(formatSince(undefined, now), '')
  assert.equal(formatSince('2026-11-26T13:00:00Z', now), '') // future: unknown, not negative

  assert.equal(enrollmentRemaining('2026-11-26T12:47:00Z', now), '剩余 47 分钟')
  assert.equal(enrollmentRemaining('2026-11-26T11:00:00Z', now), '已过期')
})

test('nodes group by management boundary, remote ones online first', () => {
  const nodes: NodeInfo[] = [
    { id: 'local', local: true, transport: 'aop', terminal: true },
    { id: 'scan_b', remote: remote({ tunnel_up: false, state: 'offline' }), agent: 'b', terminal: true } as NodeInfo,
    { id: 'scan_a', remote: remote(), agent: 'a', terminal: true } as NodeInfo,
    { id: 'fence', agent: 'fence', transport: 'aop', terminal: true },
  ]
  const groups = groupNodes(nodes)
  assert.deepEqual(groups.remote.map((n) => n.id), ['scan_a', 'scan_b'])
  assert.deepEqual(groups.direct.map((n) => n.id), ['fence'])
  assert.deepEqual(groups.local.map((n) => n.id), ['local'])
})

test('a discovered node finds its runtime row by the bare node id', () => {
  const row: RemoteNode = { id: 'abc', tunnel: { up: true, bytes_in: 1, bytes_out: 2, since: '2026-11-26T11:00:00Z' } }
  const sessions = remoteSessions([row])
  const discovered: NodeInfo = { id: 'scan_abc', agent: 'abc' }
  assert.equal(sessionOf(sessions, discovered)?.id, 'abc')
  assert.equal(sessionOf(sessions, { id: 'local' }), undefined)
})

test('only pending enrollments are waiting for a callback', () => {
  const base = { name: 'edge', recipe: 'full', deploy: 'command', created_at: '', expires_at: '' }
  const list = [
    { ...base, id: '1', state: 'pending' },
    { ...base, id: '2', state: 'joined' },
    { ...base, id: '3', state: 'expired' },
  ]
  assert.deepEqual(pendingEnrollments(list).map((e) => e.id), ['1'])
})

// nodeCardData 把心跳样本变成四张卡片的行列；不支持的计数器读作「不支持」而不是 0。
import { healthLabel, healthTone, formatUptime, nodeCardData, nodeResourceRows } from '../src/lib/remote-nodes.ts'
import type { NodeResources } from '../src/lib/types.ts'

const sample = (over: Partial<NodeResources> = {}): NodeResources => ({
  collected_at: '2026-11-26T10:00:00Z',
  cpu_percent: 37.6,
  mem_used: 6 * 1024 ** 3,
  mem_total: 16 * 1024 ** 3,
  disk_free: 4 * 1024 ** 3,
  disk_total: 80 * 1024 ** 3,
  scan_rss: 1.5 * 1024 ** 3,
  ...over,
})

const remoteNode = (over: Partial<NodeInfo> = {}): NodeInfo => ({
  id: 'scan_6032',
  name: 'verify-node',
  transport: 'aop',
  local: false,
  ...over,
})

test('nodeResourceRows renders measured counters and marks missing ones', () => {
  const rows = nodeResourceRows(sample())
  const byLabel = Object.fromEntries(rows.map((r) => [r.label, r.value]))
  assert.equal(byLabel['CPU'], '38%')
  assert.match(byLabel['内存'], /6\.0 GB \/ 16 GB/)
  assert.match(byLabel['磁盘剩余'], /4\.0 GB \/ 80 GB/)
  assert.match(byLabel['scan 内存'], /2 GB|1\.5 GB/)

  // 没有 sample（等待首次心跳）与内核不支持，都是明确的文案而不是 0。
  assert.deepEqual(nodeResourceRows(undefined), [{ label: '资源采样', value: '暂无（等待首次心跳）' }])
  const blind = nodeResourceRows({ collected_at: '2026-11-26T10:00:00Z' })
  assert.ok(blind.every((r) => r.value === '不支持'), blind.map((r) => r.value).join())
})

test('nodeCardData groups remote rows and falls back to the local sample', () => {
  const session = {
    deploy: 'command', recipe: 'full', state: 'online', tunnel_up: true, dispatch_ready: true,
    hostname: 'host-1', os: 'linux', arch: 'amd64', launcher_version: '4zrcin-9ce3f',
    uptime_seconds: 3600 * 3 + 720, last_seen_at: '2026-11-26T10:00:00Z',
    tunnel: { up: true, bytes_in: 13028, bytes_out: 5174, since: '2026-11-26T09:18:00Z' },
    resources: sample(), health: { state: 'degraded' as const, reasons: ['内存占用 91%'] },
    scan_restarts: 1,
  } as unknown as RemoteNode
  const node = remoteNode()
  const cards = nodeCardData(node, session, Date.parse('2026-11-26T10:00:00Z'))
  assert.ok(cards.identity.some((r) => r.label === '版本' && r.value === '4zrcin-9ce3f'))
  assert.ok(cards.runtime.some((r) => r.label === '隧道' && r.value === '已连接'))
  assert.equal(formatUptime(session.uptime_seconds), '3h 12m')
  assert.equal(cards.health.state, 'degraded')
  assert.deepEqual(cards.health.reasons, ['内存占用 91%'])

  // 本地节点：资源来自控制面自己的采样，身份卡不再有部署/配方。
  const local = nodeCardData({ ...node, id: 'local', local: true,
    resources: sample({ cpu_percent: 12, scan_rss: 0 }),
    health: { state: 'healthy' } }, undefined)
  assert.ok(local.identity.some((r) => r.value === '本机（控制面自带）'))
  assert.ok(local.resources.some((r) => r.label === 'CPU' && r.value === '12%'))
  assert.ok(!local.resources.some((r) => r.label === 'scan 内存'), '本地采样没有 scan pid 时不出该行')
  assert.equal(local.health.state, 'healthy')
})

test('healthLabel/healthTone keep the card to one word and one tone', () => {
  assert.equal(healthLabel('healthy'), '健康')
  assert.equal(healthLabel(''), '未知')
  assert.equal(healthTone('abnormal'), 'destructive')
  assert.equal(healthTone('degraded'), 'warning')
})
