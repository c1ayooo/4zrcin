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
let minimapSize: (bounds: { minX: number; minY: number; maxX: number; maxY: number }) => { w: number; h: number }
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
  return React.createElement(GraphMinimap, { model, onCenter: () => {}, onFit: () => {}, live })
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
  const module = await server.ssrLoadModule('/src/components/graph/GraphMinimap.tsx')
  GraphMinimap = module.GraphMinimap
  minimapSize = module.minimapSize
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
    assert.ok(x >= 3 && y >= 3 && x + width <= fakeCanvas.width / 2 - 3 && y + height <= fakeCanvas.height / 2 - 3)
  }

  frames = []
  await act(async () => {
    setView({ camera: { x: -1000, y: -1000, k: 1 }, width: 200, height: 160 })
  })
  // The card always shows the whole graph; what must never happen is a frame
  // escaping the card, however far the camera has wandered off the map.

  ops = []
  await act(async () => { renderer!.unmount() })
  await act(async () => { renderer = create(element(false), { createNodeMock: () => fakeCanvas }) })
  for (const [x, y, width, height] of frames) {
    assert.ok(x >= 3 && y >= 3 && x + width <= fakeCanvas.width / 2 - 3 && y + height <= fakeCanvas.height / 2 - 3)
  }
  assert.ok(ops.includes('clearRect'), 'the minimap still paints when the camera is off the map')
})

test('a small graph with many relations keeps its nodes without drawing an edge mesh', async () => {
  setView({ camera: { x: 0, y: 0, k: 4 }, width: 200, height: 160 })
  const crowded = { ...model, edges: Array.from({ length: 40 }, (_, id) => ({ ...model.edges[0], id })) }
  ops = []
  await act(async () => {
    renderer = create(React.createElement(GraphMinimap, { model: crowded, onCenter: () => {}, onFit: () => {} }), {
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

test('the card shows the whole graph whatever the camera does', async () => {
  // LoL 式缩略图：图本身不裁不缩、不跟镜头，只有视口方框在变。所以两个差很多的
  // 视口下，画出来的节点/骨架必须逐点相同——这才是「整个缩略图」的判据。
  const draw = async (camera: { x: number; y: number; k: number }, width: number, height: number) => {
    setView({ camera, width, height })
    edgeTargets = []
    dotRects = []
    await act(async () => {
      renderer?.unmount()
      renderer = create(element(false), { createNodeMock: () => fakeCanvas })
    })
    // Everything except the viewport box itself must be identical; the dim fill and
    // the dashed frame are the only camera-dependent ops. A second paint repeats the
    // same ops, so compare the distinct set rather than the call sequence.
    const uniq = (rows: number[][]) => [...new Set(rows.map((r) => r.join(',')))].sort()
    return JSON.stringify({
      edges: uniq(edgeTargets),
      dots: uniq(dotRects.filter(([, , w, h]) => w <= 4 && h <= 4)),
    })
  }
  const small = await draw({ x: 28, y: 20, k: 1.2 }, 200, 160)
  const huge = await draw({ x: 60, y: 24, k: 0.5 }, 1200, 600)
  assert.equal(huge, small, 'the miniature must not depend on the viewport')
})

test('the card is a fixed square whatever shape the graph has', () => {
  // 定稿：卡片尺寸恒定，不跟包围盒走。理由是力导向每帧都在改包围盒，卡片跟着变宽
  // 会一直抖；表头也会时宽时窄。整图 contain 居中，极端比例就留边。
  for (const bounds of [
    { minX: 0, minY: 0, maxX: 100, maxY: 96 },   // 近正方（力导向）
    { minX: 0, minY: 0, maxX: 36, maxY: 100 },   // 窄高（链路树）
    { minX: 0, minY: 0, maxX: 100, maxY: 36 },   // 横条
    { minX: 0, minY: 0, maxX: 500, maxY: 100 },  // 5:1 细长
    { minX: 5, minY: 5, maxX: 5, maxY: 5 },      // 退化成一个点
  ]) {
    assert.deepEqual(minimapSize(bounds), { w: 132, h: 132 }, JSON.stringify(bounds))
  }
})

test('the canvas fills the card, so no dead band sits on its right', async () => {
  // 644604b3 的实测：卡片被表头撑到 105px、画布只有 56px 且贴左边，右边 49px 死区。
  // 卡片宽度必须只有一个来源——画布。
  setView({ camera: { x: 0, y: 0, k: 4 }, width: 200, height: 160 })
  await act(async () => {
    renderer = create(element(false), { createNodeMock: () => fakeCanvas })
  })
  const canvas = renderer!.root.findByProps({ 'data-testid': 'graph-minimap' })
  const card = canvas.parent!
  const width = canvas.props.style.width
  assert.equal(card.props.style.inlineSize, width, 'the card takes its width from the canvas, not from the header')
  assert.ok(width >= 120, `the header fits on one line in ${width}px`)
})

test('switching layouts preserves the chain spine and changes direction markers', async () => {
  setView({ camera: { x: 20, y: 20, k: 1 }, width: 800, height: 600 })
  const spine = { ...model.edges[0], id: 'spine', kind: 'spawn' }
  const crowded = { ...model, edges: [spine, ...Array.from({ length: 40 }, (_, id) => ({ ...model.edges[0], id }))] }
  const forLayout = (layout: 'force' | 'layered') =>
    React.createElement(GraphMinimap, { model: crowded, onCenter: () => {}, onFit: () => {}, layout })

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

test('a double-click recenters without jumping; a drag only positions while moving', async () => {
  const centers: [number, number][] = []
  let fits = 0
  const element = () =>
    React.createElement(GraphMinimap, {
      model,
      onCenter: (x: number, y: number) => centers.push([x, y]),
      onFit: () => {
        fits += 1
      },
    })
  await act(async () => {
    renderer = create(element(), { createNodeMock: () => fakeCanvas })
  })
  const props = () => renderer!.root.findByProps({ 'data-testid': 'graph-minimap' }).props
  const target = { ...fakeCanvas, setPointerCapture() {}, releasePointerCapture() {} }
  const ev = (clientX: number, clientY: number) => ({
    clientX,
    clientY,
    pointerId: 1,
    preventDefault() {},
    currentTarget: target,
  })
  const down = (x: number, y: number) => props().onPointerDown(ev(x, y))
  const up = (x: number, y: number) => props().onPointerUp(ev(x, y))
  const move = (x: number, y: number) => props().onPointerMove(ev(x, y))

  assert.ok(String(props()['aria-label']).includes('双击'), 'the hint names the double-click gesture')

  // 单击抬手后要等一个双击窗口才落地，这一下还不能定位。
  down(20, 20)
  up(20, 20)
  assert.equal(centers.length, 0, 'a single click waits for the double-click window')

  // 双击：第二下先取消待执行的单击，再回正中，全程不跳到点上。
  down(20, 20)
  up(20, 20)
  props().onDoubleClick({ preventDefault() {} })
  assert.equal(fits, 1, 'a double-click recenters the view')
  await new Promise((resolve) => setTimeout(resolve, 350))
  assert.equal(centers.length, 0, 'a double-click must not jump to the clicked point first')

  // 单独的单击在窗口过后定位。
  down(30, 30)
  up(30, 30)
  await new Promise((resolve) => setTimeout(resolve, 350))
  assert.equal(centers.length, 1, 'a lone click positions the view')

  // 拖动：移动中跟手定位，抬手不再补一次。
  down(40, 40)
  move(60, 60)
  move(80, 80)
  assert.equal(centers.length, 3, 'dragging positions the view while moving')
  up(80, 80)
  await new Promise((resolve) => setTimeout(resolve, 350))
  assert.equal(centers.length, 3, 'lifting after a drag must not add another jump')
})
