// 边聚合是纯绘制层的函数：798 条扇出边收成每个方向一条折线。这里钉住三件事——
// 折线按极角串起来、非扇出边原样保留、链路树不聚合（读链视图要逐边箭头）。
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let mod: {
  aggregateFan: (
    edges: ReadonlyArray<Record<string, unknown>>,
    nodes: ReadonlyArray<Record<string, unknown>>,
  ) => { edges: Array<Record<string, unknown>>; uncovered: string[]; chains: number; absorbed: number }
  aggregateForDraw: (
    edges: ReadonlyArray<Record<string, unknown>>,
    nodes: ReadonlyArray<Record<string, unknown>>,
    layered: boolean,
  ) => unknown
}

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  mod = (await server.ssrLoadModule('/src/graph/aggregate.ts')) as typeof mod
})

after(async () => {
  await server.close()
})

const node = (id: string, x: number, y: number) => ({ id, type: id.startsWith('f:') ? 'fact' : 'step', label: id, x, y })

test('a direction fan becomes one chain ordered around the direction', () => {
  const nodes = [
    node('s:a', 0, 0),
    node('f:up', 0, -100),
    node('f:right', 100, 0),
    node('f:left', -100, 0),
  ]
  const edges = [
    { id: 'e1', source: 's:a', target: 'f:right', kind: 'intent' },
    { id: 'e2', source: 's:a', target: 'f:left', kind: 'intent' },
    { id: 'e3', source: 's:a', target: 'f:up', kind: 'intent' },
  ]
  const out = mod.aggregateFan(edges, nodes)
  assert.equal(out.chains, 1)
  assert.equal(out.absorbed, 3)
  assert.equal(out.uncovered.length, 0)
  // 折线按极角升序串起来（-π/2 上 → 0 右 → π 左），首段从方向节点出发。
  assert.deepEqual(
    out.edges.map((e) => [e.source, e.target]),
    [
      ['s:a', 'f:up'],
      ['f:up', 'f:right'],
      ['f:right', 'f:left'],
    ],
  )
  assert.deepEqual(
    out.edges.map((e) => e.chainIndex),
    [0, 1, 2],
  )
})

test('other relation kinds keep their own line', () => {
  const nodes = [node('s:a', 0, 0), node('f:b', 10, 0), node('f:c', 0, 10)]
  const edges = [
    { id: 'fan', source: 's:a', target: 'f:b', kind: 'intent' },
    { id: 'gate', source: 'f:c', target: 'f:b', kind: 'premise' },
  ]
  const out = mod.aggregateFan(edges, nodes)
  const kept = out.edges.find((e) => e.id === 'gate')
  assert.ok(kept, 'premise edge survives aggregation')
  assert.equal(kept?.kind, 'premise')
  assert.equal(out.chains, 1)
})

test('the chain tree is never aggregated', () => {
  const nodes = [node('s:a', 0, 0), node('f:b', 10, 0)]
  const edges = [{ id: 'fan', source: 's:a', target: 'f:b', kind: 'intent' }]
  assert.equal(mod.aggregateForDraw(edges, nodes, true), null)
  assert.ok(mod.aggregateForDraw(edges, nodes, false))
})
