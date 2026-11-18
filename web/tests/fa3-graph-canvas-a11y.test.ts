// FA3 accessibility contract for the canvas.
//
// The map is painted pixels, so the DOM around it is the only thing a screen
// reader or a keyboard user ever receives: the canvas must be focusable, named,
// described (shortcut help), and it must expose a polite live region for the
// keyboard ring the sighted user sees. This suite renders the real component
// with stub host globals — react-test-renderer gives no DOM, so refs are null
// and the paint path is inert; only the rendered accessibility surface is
// asserted.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

const original = {
  window: Object.getOwnPropertyDescriptor(globalThis, 'window'),
  document: Object.getOwnPropertyDescriptor(globalThis, 'document'),
  raf: Object.getOwnPropertyDescriptor(globalThis, 'requestAnimationFrame'),
  caf: Object.getOwnPropertyDescriptor(globalThis, 'cancelAnimationFrame'),
}

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphCanvas: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

const model = {
  nodes: [
    {
      id: 's:in_1',
      type: 'step',
      label: '验证边界',
      status: 'running',
      x: 0,
      y: 0,
      vx: 0,
      vy: 0,
      degree: 1,
      w: 60,
      h: 20,
      depth: 1,
    },
  ],
  edges: [],
  byId: new Map(),
  edgesById: new Map(),
} as any
model.byId.set('s:in_1', model.nodes[0])

function installHostStubs() {
  const media = {
    matches: false,
    media: '',
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      devicePixelRatio: 2,
      innerWidth: 1440,
      innerHeight: 900,
      matchMedia: () => media,
      addEventListener() {},
      removeEventListener() {},
      localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
      location: { protocol: 'http:', host: '127.0.0.1:1' },
    },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      visibilityState: 'visible',
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
  GraphCanvas = (await server.ssrLoadModule('/src/components/GraphCanvas.tsx')).GraphCanvas
  installHostStubs()
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

async function mountCanvas(overrides: Record<string, unknown> = {}) {
  await act(async () => {
    renderer = create(
      React.createElement(GraphCanvas, {
        model,
        selected: null,
        matches: null,
        showLabels: false,
        layout: 'layered',
        onSelect: () => {},
        ...overrides,
      }),
    )
  })
}

function canvasProps(): any {
  const node = renderer!.root.findAll((candidate) => candidate.type === ('canvas' as any))[0]
  assert.ok(node, 'the canvas is rendered')
  return node!.props
}

test('the canvas is focusable, named, and described by the shortcut help', async () => {
  await mountCanvas()
  const props = canvasProps()
  assert.equal(props.tabIndex, 0, 'the canvas is a tab stop')
  assert.equal(props.role, 'application', 'the map is a keyboard-operable application surface')
  assert.equal(props['aria-roledescription'], '交互式图谱')
  assert.equal(props['data-testid'], 'graph-canvas')
  // The name carries the map's size and the current selection…
  assert.match(props['aria-label'], /1 个节点/)
  assert.match(props['aria-label'], /当前未选中节点/)
  // …the description carries the keyboard model, so it can be re-read on demand.
  assert.ok(props['aria-describedby'], 'shortcuts are an accessible description')
  const help = renderer!.root.findAll((candidate) => candidate.type === ('p' as any))
  const helpNode = help.find((candidate) => candidate.props.id === props['aria-describedby'])
  assert.ok(helpNode, 'the described-by target exists')
  const helpText = String(JSON.stringify(helpNode!.children.join('')))
  assert.match(helpText, /方向键/)
  assert.match(helpText, /Enter/)
  assert.match(helpText, /Escape/)
  // Modifier shortcuts are announced by assistive tech from this attribute.
  assert.match(props['aria-keyshortcuts'], /ArrowRight/)
  assert.match(props['aria-keyshortcuts'], /Home/)
  assert.match(props['aria-keyshortcuts'], /Escape/)
  // A keyboard user must see where focus is: the ring is an outline, not a
  // suppressed native indicator.
  assert.match(props.className, /focus-visible:outline/)
})

test('a polite live region reports the keyboard ring position', async () => {
  await mountCanvas()
  const live = renderer!.root.findAll((candidate) => candidate.props['aria-live'] === 'polite')
  assert.equal(live.length, 1, 'exactly one live region (screen-reader chatter stays bounded)')
  assert.equal(live[0].props['aria-atomic'], 'true')
  assert.equal(String(live[0].props.className).includes('sr-only'), true, 'visually hidden, not removed')
})

test('the selected node is reflected in the canvas name', async () => {
  await mountCanvas({ selected: 's:in_1' })
  assert.match(canvasProps()['aria-label'], /当前选中：验证边界/)
})
