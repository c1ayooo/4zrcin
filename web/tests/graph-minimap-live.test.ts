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
let setView: (view: { camera: { x: number; y: number; k: number }; width: number; height: number }) => void
let renderer: ReactTestRenderer | undefined
let ops: string[] = []

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
      void args
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
    { id: 'f:1', type: 'fact', label: '事实', x: 0, y: 0, w: 40, h: 16, status: 'verified' },
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
