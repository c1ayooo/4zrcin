// The minimap's live rings belong to the live frames only. When the last
// running direction settles, `live` flips to false while the camera and the
// model stay put — the repaint has to follow that flag, or the last breathing
// rings stay frozen on the canvas until the operator pans or zooms.
//
// react-test-renderer has no DOM, so a fake canvas is injected through
// createNodeMock and the 2D context records what the component drew.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

const original = {
  window: Object.getOwnPropertyDescriptor(globalThis, 'window'),
  raf: Object.getOwnPropertyDescriptor(globalThis, 'requestAnimationFrame'),
  caf: Object.getOwnPropertyDescriptor(globalThis, 'cancelAnimationFrame'),
}

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphMinimap: React.ComponentType<any>
let setView: (view: { camera: { x: number; y: number; k: number }; width: number; height: number; layoutRevision?: number }) => void
let renderer: ReactTestRenderer | undefined
let ops: string[] = []
let frames: number[][] = []
let edgeTargets: number[][] = []
let dotRects: number[][] = []

function fakeContext() {
  const ctx: Record<string, unknown> = {}
  for (const name of [
    'setTransform',
    'clearRect',
    'fillRect',
    'strokeRect',
    'beginPath',
    'rect',
    'moveTo',
    'lineTo',
    'arc',
    'closePath',
    'fill',
    'stroke',
    'setLineDash',
    'save',
    'restore',
  ]) {
    ctx[name] = (...args: unknown[]) => {
      ops.push(name)
      if (name === 'strokeRect') frames.push(args as number[])
      if (name === 'lineTo') edgeTargets.push(args as number[])
      if (name === 'fillRect') dotRects.push(args as number[])
    }
  }
  return ctx
}

const ctx = fakeContext()
const fakeCanvas = {
  width: 0,
  height: 0,
  style: {},
  getContext: () => ctx,
  getBoundingClientRect: () => ({ left: 0, top: 0, width: 184, height: 116, right: 184, bottom: 116 }),
  addEventListener() {},
  removeEventListener() {},
}

const model = {
  nodes: [
    { id: 'f:1', type: 'fact', label: '事实', x: 0, y: 0, w: 40, h: 16, degree: 1, status: 'verified' },
    { id: 's:1', type: 'step', label: '方向', x: 120, y: 80, w: 40, h: 16, status: 'running' },
  ],
  edges: [{ id: 'e:1', source: 'f:1', target: 's:1', kind: 'intent', status: 'running' }],
  byId: new Map(),
  edgesById: new Map(),
} as any
for (const node of model.nodes) model.byId.set(node.id, node)

function element(live: boolean) {
  return React.createElement(GraphMinimap, { model, onCenter: () => {}, live })
}

function installHostStubs() {
  const media = {
    matches: false,
    media: '',
    addEventListener() {},
    removeEventListener() {},
  }
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      devicePixelRatio: 2,
      matchMedia: () => media,
      addEventListener() {},
      removeEventListener() {},
    },
  })
  Object.defineProperty(globalThis, 'requestAnimationFrame', { configurable: true, value: () => 0 })
  Object.defineProperty(globalThis, 'cancelAnimationFrame', { configurable: true, value: () => {} })
}

function restoreHostStubs() {
  for (const [key, descriptor] of Object.entries(original)) {
    const name = key === 'raf' ? 'requestAnimationFrame' : key === 'caf' ? 'cancelAnimationFrame' : key
    if (descriptor) Object.defineProperty(globalThis, name, descriptor)
    else Reflect.deleteProperty(globalThis, name)
  }
}

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GraphMinimap = (await server.ssrLoadModule('/src/components/graph/GraphMinimap.tsx')).GraphMinimap
  setView = (await server.ssrLoadModule('/src/graph/view-store.ts')).setView
  installHostStubs()
  // A zoomed-in camera, so the viewport rectangle is smaller than the graph and
  // the component draws the frame instead of hiding it as a full-map view.
  setView({ camera: { x: 0, y: 0, k: 4 }, width: 200, height: 160 })
})

afterEach(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
})

after(async () => {
  restoreHostStubs()
  await server?.close()
})

test('leaving the live state repaints once and drops the breathing rings', async () => {
  ops = []
  await act(async () => {
    renderer = create(element(true), { createNodeMock: () => fakeCanvas })
  })
  assert.ok(ops.includes('clearRect'), 'the minimap painted on mount')
  assert.ok(
    ops.filter((op) => op === 'arc').length >= 2,
    'a live minimap draws a ring and a centre dot around the running direction',
  )
  assert.ok(ops.includes('lineTo'), 'the relation skeleton is drawn')
  assert.ok(ops.includes('strokeRect'), 'the viewport frame is drawn')

  // The last run settles: live flips to false, the camera and model are unchanged.
  ops = []
  await act(async () => {
    renderer!.update(element(false))
  })
  assert.ok(ops.includes('clearRect'), 'the minimap repainted for the new live state')
  assert.ok(!ops.includes('arc'), 'the live rings are gone from the settled frame')
  assert.ok(ops.includes('lineTo'), 'the relation skeleton survives the repaint')
  assert.ok(ops.includes('strokeRect'), 'the viewport rectangle is still drawn')
})

test('a view that already shows the whole graph hides the viewport frame', async () => {
  // The fit view covers the graph entirely: drawing the frame used to leave a
  // single stray edge at the canvas border. There is nothing to locate, so the
  // frame is dropped rather than clamped to a meaningless box.
  setView({ camera: { x: 20, y: 20, k: 1 }, width: 800, height: 600 })
  ops = []
  await act(async () => {
    renderer = create(element(false), { createNodeMock: () => fakeCanvas })
  })
  assert.ok(ops.includes('clearRect'), 'the minimap painted on mount')
  assert.ok(!ops.includes('strokeRect'), 'no viewport frame on a full-map view')
  assert.ok(ops.includes('fillRect'), 'nodes are still drawn')
})

test('a clipped viewport stays inside the card, and an off-map one still shows where the camera is', async () => {
  setView({ camera: { x: -10, y: 0, k: 1 }, width: 400, height: 400 })
  frames = []
  await act(async () => {
    renderer = create(element(false), { createNodeMock: () => fakeCanvas })
  })
  assert.ok(frames.length > 0)
  for (const [x, y, width, height] of frames) {
    assert.ok(x >= 3 && y >= 3 && x + width <= 181 && y + height <= 113)
  }

  frames = []
  await act(async () => {
    setView({ camera: { x: -1000, y: -1000, k: 1 }, width: 200, height: 160 })
  })
  // With bounds = world ∪ viewport the card always has content to show, but at
  // this distance the viewport sits outside the covered area, so it may be
  // clipped away entirely. What must never happen is a frame escaping the card.
  ops = []
  await act(async () => { renderer!.unmount() })
  await act(async () => { renderer = create(element(false), { createNodeMock: () => fakeCanvas }) })
  for (const [x, y, width, height] of frames) {
    assert.ok(x >= 3 && y >= 3 && x + width <= 181 && y + height <= 113)
  }
  assert.ok(ops.includes('clearRect'), 'the minimap still paints when the camera is off the map')
})

test('a small graph with many relations keeps its nodes without drawing an edge mesh', async () => {
  setView({ camera: { x: 0, y: 0, k: 4 }, width: 200, height: 160 })
  const crowded = { ...model, edges: Array.from({ length: 40 }, (_, id) => ({ ...model.edges[0], id })) }
  ops = []
  await act(async () => {
    renderer = create(React.createElement(GraphMinimap, { model: crowded, onCenter: () => {} }), {
      createNodeMock: () => fakeCanvas,
    })
  })
  assert.ok(ops.includes('fillRect'))
  assert.ok(ops.includes('strokeRect'))
  assert.ok(!ops.includes('stroke'), 'edges are omitted for the crowded miniature')
})

test('the miniature follows node movement even when the camera does not move', async () => {
  const view = { camera: { x: 0, y: 0, k: 4 }, width: 200, height: 160, layoutRevision: 1 }
  setView(view)
  edgeTargets = []
  await act(async () => {
    renderer = create(element(false), { createNodeMock: () => fakeCanvas })
  })
  const before = edgeTargets[0]
  assert.ok(before, 'the edge was drawn')

  const node = model.nodes[1]
  const previousX = node.x
  edgeTargets = []
  try {
    node.x = previousX + 180
    await act(async () => setView({ ...view, layoutRevision: 2 }))
    assert.ok(edgeTargets[0], 'layout changes repaint without a camera change')
    assert.notDeepEqual(edgeTargets[0], before, 'relations follow the moved node')
  } finally {
    node.x = previousX
  }
})

test('a viewport far larger than the graph frames the graph, not the empty stage', async () => {
  // 1280 实测（`.build/ui-qa/graph-final`，合成数据）：视口比图宽高两三倍时，直接取
  // union 会把画面里那一圈空台面当成内容，图只占卡片 33% 高、空区 73.5%——正是操作员
  // 说的「两百是空白」。逐轴夹进世界范围后，同一个视口应让图铺满卡片。
  // Viewport [-20,380]×[-8,192]: covers the graph and is still 2× taller than it,
  // the shape a wide desktop stage puts around a flat graph.
  setView({ camera: { x: 60, y: 24, k: 3 }, width: 1200, height: 600 })
  edgeTargets = []
  dotRects = []
  await act(async () => {
    renderer = create(element(false), { createNodeMock: () => fakeCanvas })
  })
  // Nodes land as small fill dots and diamonds; take every drawn marker position.
  const ys = [
    ...dotRects.filter(([, , w, h]) => w <= 4 && h <= 4).map(([, y]) => y),
    ...edgeTargets.map(([, y]) => y),
  ]
  assert.ok(ys.length >= 2, 'both nodes are drawn')
  const span = Math.max(...ys) - Math.min(...ys)
  assert.ok(span >= 116 * 0.65, `the graph fills the card height (span ${span.toFixed(1)} of 116)`)
})

test('switching layouts preserves the chain spine and changes direction markers', async () => {
  setView({ camera: { x: 20, y: 20, k: 1 }, width: 800, height: 600 })
  const spine = { ...model.edges[0], id: 'spine', kind: 'spawn' }
  const crowded = { ...model, edges: [spine, ...Array.from({ length: 40 }, (_, id) => ({ ...model.edges[0], id }))] }
  const forLayout = (layout: 'force' | 'layered') =>
    React.createElement(GraphMinimap, { model: crowded, onCenter: () => {}, layout })

  ops = []
  await act(async () => {
    renderer = create(forLayout('force'), { createNodeMock: () => fakeCanvas })
  })
  assert.ok(ops.includes('closePath'), 'force directions use diamonds')
  assert.ok(!ops.includes('stroke'), 'the force view omits the edge mesh')

  ops = []
  await act(async () => renderer!.update(forLayout('layered')))
  assert.ok(!ops.includes('closePath'), 'chain directions use bars')
  assert.equal(ops.filter((op) => op === 'stroke').length, 1, 'only the chain spine is drawn')
})
