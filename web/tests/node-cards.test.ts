// The node detail cards (docs/14 §7.3) render through the same SSR harness as
// the other components: four quiet cards, the reason list only when degraded.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import { nodeCardData } from '../src/lib/remote-nodes.ts'
import type { NodeInfo, RemoteNode } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let NodeCards: React.ComponentType<any>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  NodeCards = (await server.ssrLoadModule('/src/components/NodeCards.tsx')).NodeCards
})

after(async () => {
  await server?.close()
})

const session = {
  deploy: 'command', recipe: 'full', state: 'online', tunnel_up: true, dispatch_ready: true,
  name: 'verify-node', hostname: 'host-1', os: 'linux', arch: 'amd64',
  launcher_version: '4zrcin-9ce3f', uptime_seconds: 3600 * 3 + 720,
  last_seen_at: '2026-11-26T10:00:00Z',
  tunnel: { up: true, bytes_in: 13028, bytes_out: 5174, since: '2026-11-26T09:18:00Z' },
  resources: { collected_at: '2026-11-26T10:00:00Z', cpu_percent: 37.6,
    mem_used: 6 * 1024 ** 3, mem_total: 16 * 1024 ** 3,
    disk_free: 4 * 1024 ** 3, disk_total: 80 * 1024 ** 3, scan_rss: 1.5 * 1024 ** 3 },
  health: { state: 'degraded', reasons: ['内存占用 91%'] },
  scan_restarts: 1,
} as unknown as RemoteNode

test('an expanded remote row renders the four cards with the sample', () => {
  const cards = nodeCardData({ id: 'scan_6032', name: 'verify-node', transport: 'aop' } as NodeInfo, session)
  const data = { ...cards, tone: 'warning' as const, label: '降级' }
  const html = renderToStaticMarkup(React.createElement(NodeCards, { data, ariaLabel: 'verify-node 的详情卡片' }))
  for (const text of ['身份', '运行', '资源', '健康', '降级', '内存占用 91%', '已连接', '4zrcin-9ce3f']) {
    assert.ok(html.includes(text), `missing ${text}`)
  }
  assert.ok(html.includes('region'), 'the card group is a labelled region')
})

test('a node without a sample says so instead of showing zeros', () => {
  const cards = nodeCardData({ id: 'local', local: true, transport: 'aop' } as NodeInfo, undefined)
  const data = { ...cards, tone: 'muted' as const, label: '未知' }
  const html = renderToStaticMarkup(React.createElement(NodeCards, { data, ariaLabel: 'local 的详情卡片' }))
  assert.ok(html.includes('暂无采样，等待首次心跳'))
  assert.ok(html.includes('本机（控制面自带）'))
  // The rings render a grey dash per missing counter (three of them).
  // Three dash rings (one per missing counter); the sub lines render empty.
  assert.ok(html.includes('—'), 'the rings must render dashes for missing counters')
})

test('a node whose egress change has not applied yet says so on its card', () => {
  const node = { id: 'scan_6032', name: 'verify-node', transport: 'aop' } as NodeInfo
  const pending = nodeCardData(node, { ...session, proxy_pending: true } as RemoteNode)
  const html = renderToStaticMarkup(React.createElement(NodeCards, {
    data: { ...pending, tone: 'warning' as const, label: '降级' }, ariaLabel: 'verify-node 的详情卡片',
  }))
  for (const text of ['出网代理', '变更待生效']) {
    assert.ok(html.includes(text), `the card must say the node is held for its egress change: missing ${text}`)
  }
  // A node running the platform's default shows no such line.
  const applied = nodeCardData(node, session)
  assert.ok(!renderToStaticMarkup(React.createElement(NodeCards, {
    data: { ...applied, tone: 'warning' as const, label: '降级' }, ariaLabel: 'verify-node 的详情卡片',
  })).includes('变更待生效'))
})
