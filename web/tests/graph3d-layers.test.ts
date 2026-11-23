// 3D 星图的布局是「按类型分层」，不是三维力导向：这里钉住层序、确定性，以及
// 算法 那把尺子要的 ForceSim 契约（tick/running/nodes.{id,x,y,z}）。
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let layers: typeof import('../src/graph3d/layers.ts')
let sim3d: typeof import('../src/graph3d/sim3d.ts')

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  layers = (await server.ssrLoadModule('/src/graph3d/layers.ts')) as typeof layers
  sim3d = (await server.ssrLoadModule('/src/graph3d/sim3d.ts')) as typeof sim3d
})

after(async () => {
  await server.close()
})

const node = (id: string, type: string, degree = 0, status?: string) =>
  ({ id, type, status, degree, x: 0, y: 0, w: 20, h: 8 }) as never

test('the third axis is structure: project above goals above directions above facts', () => {
  const z = (n: unknown) => layers.layeredZ(n as never)
  const project = z(node('p:1', 'project'))
  const goal = z(node('g:1', 'goal'))
  const step = z(node('s:1', 'step'))
  const ghost = z(node('s:2', 'ghost'))
  const fact = z(node('f:1', 'fact'))
  const boundary = z(node('b:1', 'boundary'))
  assert.ok(project > goal, `project sits above goals (${project.toFixed(0)} > ${goal.toFixed(0)})`)
  assert.ok(goal > step, 'goals sit above directions')
  // 层内还有 ±34 的确定性微扰，所以比较「层号」而不是抖动后的 z 值。
  assert.ok(
    layers.layerOf({ type: 'step' } as never) > layers.layerOf({ type: 'ghost' } as never),
    'directions sit above their pending ends',
  )
  assert.ok(ghost > fact, 'directions sit above facts')
  assert.ok(fact > boundary, 'facts sit above boundary leaves')
  // 层间距离至少是层高的一半，层不会被微扰糊在一起。
  assert.ok(goal - fact > layers.LAYER_HEIGHT * 0.5, 'layers stay visually separated')
})

test('z is deterministic, jitter stays in bounds and degree lifts within a layer', () => {
  const a = layers.layeredZ(node('f:42', 'fact', 3))
  const b = layers.layeredZ(node('f:42', 'fact', 3))
  assert.equal(a, b, 'the same node always lands at the same height')
  const base = layers.LAYER_HEIGHT * layers.layerOf({ type: 'fact', status: undefined } as never)
  assert.ok(Math.abs(a - base) <= layers.LAYER_JITTER + 1, 'jitter stays within its bound')
  const low = layers.layeredZ(node('f:7', 'fact', 0))
  const high = layers.layeredZ(node('f:8', 'fact', 24))
  assert.ok(high - low > 10, 'a hub sits higher than a leaf in the same layer')
})

test('ForceSim keeps the contract 算法 measures and settles', async () => {
  const model = {
    nodes: [
      node('p:1', 'project'),
      ...Array.from({ length: 12 }, (_, i) => node(`f:${i}`, 'fact', i % 4)),
      ...Array.from({ length: 4 }, (_, i) => node(`s:${i}`, 'step')),
    ],
    edges: Array.from({ length: 16 }, (_, i) => ({ id: `e:${i}`, source: `f:${i % 12}`, target: `s:${i % 4}`, kind: 'intent' })),
    byId: new Map(),
    edgesById: new Map(),
    maxDepth: 1,
    stepByIntent: new Map(),
  } as never
  const sim = new sim3d.ForceSim(model, { mode: '3d' })
  assert.equal(sim.nodes.length, 17, 'every node gets a 3D node')
  for (const n of sim.nodes) {
    assert.ok(Number.isFinite(n.x) && Number.isFinite(n.y) && Number.isFinite(n.z), 'coordinates stay finite')
  }
  let guard = 0
  while (sim.running && guard < 2000) {
    sim.tick()
    guard += 1
  }
  assert.equal(sim.running, false, 'the 2D layout settles; the 3D view is not forever-running')
  const zs = sim.nodes.map((n) => n.z)
  assert.ok(Math.max(...zs) - Math.min(...zs) > layers.LAYER_HEIGHT, 'the picture really uses three layers')
})
