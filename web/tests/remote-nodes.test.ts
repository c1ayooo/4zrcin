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
