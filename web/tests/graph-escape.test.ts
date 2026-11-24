// 图谱里「关掉浮层」的两条硬要求（2026-11-24 巡查）：
//   1) 按钮上写「关闭（Esc）」就真接 Esc，焦点在浮层里时也生效；
//   2) 关掉之后焦点回主画布，别掉到 BODY（否则下一次 Tab 从文档头开始）。
//
// 断言拆两层：`isTypingTarget` 的边界（正在写干预文本时 Esc 不许关），以及链路条
// 这一份实际接线（Esc → 清除 + 焦点回画布）；节点详情用的是同一套 helper 与同一种
// 接线。画布与 document 用最小桩，测试不依赖真实 DOM。
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

class FakeElement {
  tagName: string
  isContentEditable: boolean
  focusedWith: unknown = undefined
  constructor(tagName: string, isContentEditable = false) {
    this.tagName = tagName
    this.isContentEditable = isContentEditable
  }
  focus(options?: unknown) {
    this.focusedWith = options
  }
}

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphChainStrip: any
let isTypingTarget: (target: unknown) => boolean
let isEscapeToClose: (event: unknown) => boolean
let closeAndFocusCanvas: (close: () => void) => void
const frames: Array<() => void> = []
let canvas: FakeElement | undefined
let queriedSelector = ''
const savedGlobals = new Map<string, PropertyDescriptor | undefined>()

before(async () => {
  for (const name of ['window', 'document', 'HTMLElement', 'requestAnimationFrame']) {
    savedGlobals.set(name, Object.getOwnPropertyDescriptor(globalThis, name))
  }
  Object.defineProperty(globalThis, 'HTMLElement', { configurable: true, value: FakeElement })
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { requestAnimationFrame: (cb: () => void) => frames.push(cb) },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      querySelector(selector: string) {
        queriedSelector = selector
        return canvas ?? null
      },
    },
  })

  server = await createTestServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [react()],
    esbuild: false,
    resolve: {
      alias: [
        { find: '@cyber/ui', replacement: fileURLToPath(new URL('../packages/ui/src', import.meta.url)) },
        { find: '@cyber/theme', replacement: fileURLToPath(new URL('../packages/theme/src', import.meta.url)) },
        { find: '@', replacement: fileURLToPath(new URL('../src', import.meta.url)) },
      ],
      dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime'],
    },
  })
  GraphChainStrip = (await server.ssrLoadModule('/src/components/graph/GraphChainStrip.tsx')).GraphChainStrip
  const focus = await server.ssrLoadModule('/src/components/graph/focus.ts')
  isTypingTarget = focus.isTypingTarget
  isEscapeToClose = focus.isEscapeToClose
  closeAndFocusCanvas = focus.closeAndFocusCanvas
})

after(() => {
  for (const [name, descriptor] of savedGlobals) {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor)
    else delete (globalThis as Record<string, unknown>)[name]
  }
})

test('isTypingTarget only claims elements that really take text', () => {
  assert.equal(isTypingTarget(new FakeElement('INPUT')), true)
  assert.equal(isTypingTarget(new FakeElement('TEXTAREA')), true)
  assert.equal(isTypingTarget(new FakeElement('SELECT')), true)
  assert.equal(isTypingTarget(new FakeElement('DIV', true)), true)
  assert.equal(isTypingTarget(new FakeElement('BUTTON')), false, 'a button does not swallow Esc')
  assert.equal(isTypingTarget(new FakeElement('CANVAS')), false)
  assert.equal(isTypingTarget(null), false)
})

function mountStrip(onClear: () => void): ReactTestRenderer {
  let renderer: ReactTestRenderer | undefined
  act(() => {
    renderer = create(
      React.createElement(GraphChainStrip, {
        hops: 7,
        near: 3,
        coverage: 559,
        treeActive: false,
        treeSize: null,
        onOnlyTree: () => {},
        onShowAll: () => {},
        onClear,
      }),
    )
  })
  assert.ok(renderer)
  return renderer
}

const strip = (r: ReactTestRenderer) =>
  r.root.findAll(
    (node) => node.type === 'div' && String(node.props.className ?? '').includes('tw-graph-chainstrip'),
  )[0]

function escapeEvent(target: unknown) {
  const event = {
    key: 'Escape',
    target,
    defaultPrevented: false,
    prevented: false,
    preventDefault() {
      event.prevented = true
      event.defaultPrevented = true
    },
  }
  return event
}

test('isEscapeToClose leaves Esc to a layer above, and to the draft being typed', () => {
  const button = new FakeElement('BUTTON')
  assert.equal(isEscapeToClose({ key: 'Escape', defaultPrevented: false, target: button }), true)
  assert.equal(isEscapeToClose({ key: 'Enter', defaultPrevented: false, target: button }), false)
  // 证据预览/确认框这类 Portal 弹层：Radix 在 document 捕获阶段已经处理并 preventDefault，
  // 面板不能再关一次（2026-11-24 实测：否则一次 Esc 关掉两层）。
  assert.equal(
    isEscapeToClose({ key: 'Escape', defaultPrevented: true, target: button }),
    false,
    'a portal dialog that already handled Esc must win',
  )
  assert.equal(isEscapeToClose({ key: 'Escape', defaultPrevented: false, target: new FakeElement('INPUT') }), false)
})

test('closeAndFocusCanvas closes once and hands focus to the canvas', () => {
  let closed = 0
  canvas = new FakeElement('CANVAS')
  frames.length = 0
  closeAndFocusCanvas(() => {
    closed += 1
  })
  assert.equal(closed, 1)
  assert.equal(frames.length, 1, 'focus is handed back on the next frame')
  act(() => {
    frames.shift()?.()
  })
  assert.match(queriedSelector, /graph-canvas/)
  assert.deepEqual(canvas?.focusedWith, { preventScroll: true })
})

test('Escape in the chain strip clears the selection and returns focus to the canvas', () => {
  let cleared = 0
  const renderer = mountStrip(() => {
    cleared += 1
  })
  canvas = new FakeElement('CANVAS')
  frames.length = 0
  const event = escapeEvent(new FakeElement('BUTTON'))

  act(() => {
    strip(renderer).props.onKeyDown(event)
  })

  assert.equal(cleared, 1, 'Esc must clear the selection')
  assert.equal(event.prevented, true, 'Esc is handled here, so the page must not also act on it')
  assert.equal(frames.length, 1, 'focus is handed back on the next frame')
  act(() => {
    frames.shift()?.()
  })
  assert.match(queriedSelector, /graph-canvas/, 'focus goes to the canvas, not to BODY')
  assert.deepEqual(canvas?.focusedWith, { preventScroll: true })

  // 按钮上的「清除选择（Esc）」也一样：清除并交回焦点。
  frames.length = 0
  act(() => {
    renderer.root
      .findAll((node) => node.type === 'button')
      .find((node) => node.props['aria-label'] === '清除选择')
      ?.props.onClick()
  })
  assert.equal(cleared, 2)
  assert.equal(frames.length, 1)
})

test('Escape while typing a note does not close the strip', () => {
  let cleared = 0
  const renderer = mountStrip(() => {
    cleared += 1
  })
  const event = escapeEvent(new FakeElement('TEXTAREA'))
  act(() => {
    strip(renderer).props.onKeyDown(event)
  })
  assert.equal(cleared, 0, 'a draft must survive Esc')
  assert.equal(event.prevented, false)
})
