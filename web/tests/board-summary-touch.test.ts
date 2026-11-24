// 账本计数面板的展开路径。
//
// 用户环境是 Firefox + 触屏：那里一次 tap 可能不送 `click`，也可能送两次（被当成双击），
// 所以「按下即开、随后的 click 忽略」是主要路径，CSS 的 hover/focus-within 是补充。
// 断言：touch pointerdown 打开、紧随的两次 click 不把它关掉、鼠标 click 正常开合、
// Esc 收起且下一次聚焦恢复、五行真数据 + sr-only「快照已载 …」常在。
import { before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let BoardSummary: any

const listeners = new Map<string, Set<(event: any) => void>>()
const documentStub = {
  addEventListener(type: string, fn: (event: any) => void) {
    if (!listeners.has(type)) listeners.set(type, new Set())
    listeners.get(type)!.add(fn)
  },
  removeEventListener(type: string, fn: (event: any) => void) {
    listeners.get(type)?.delete(fn)
  },
}
function dispatch(type: string, event: any) {
  for (const fn of listeners.get(type) ?? []) fn(event)
}

before(async () => {
  Object.defineProperty(globalThis, 'document', { configurable: true, value: documentStub })
  server = await createTestServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [react()],
    esbuild: false,
    resolve: {
      alias: [{ find: '@', replacement: fileURLToPath(new URL('../src', import.meta.url)) }],
      dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime'],
    },
  })
  BoardSummary = (await server.ssrLoadModule('/src/components/BoardCounters.tsx')).BoardSummary
})

// Real shapes from the live project (88 intents / 468 facts / 559 nodes).
const board = {
  intents: { total: 88, running: 0 },
  facts: { total: 468 },
  evidence: 7027,
} as any

function mount(): ReactTestRenderer {
  let renderer: ReactTestRenderer | undefined
  act(() => {
    renderer = create(
      React.createElement(BoardSummary, { board, running: 0, loadedNodes: 559, totalNodes: 559 }),
    )
  })
  assert.ok(renderer)
  return renderer
}

const panelClass = (renderer: ReactTestRenderer) =>
  String(renderer.root.findByProps({ 'data-testid': 'board-counts-panel' }).props.className)
const trigger = (renderer: ReactTestRenderer) => renderer.root.findByType('button')

test('a touch pointerdown opens the panel and the clicks that follow do not close it', () => {
  const renderer = mount()
  assert.match(panelClass(renderer), /\bopacity-0\b/)

  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'touch' })
  })
  assert.match(panelClass(renderer), /\bopacity-100\b/, 'the touch press must open the panel')
  assert.doesNotMatch(panelClass(renderer), /\bopacity-0\b/)
  // No fabricated expanded state: the panel can also be revealed by CSS hover/focus,
  // so `aria-expanded` would be false while it is visibly open.
  assert.equal(trigger(renderer).props['aria-expanded'], undefined)

  // Firefox may deliver one or two clicks after a tap; neither may toggle it back.
  act(() => {
    trigger(renderer).props.onClick({})
  })
  act(() => {
    trigger(renderer).props.onClick({})
  })
  assert.match(panelClass(renderer), /\bopacity-100\b/, 'trailing clicks must be ignored')
})

test('a mouse click toggles the panel, and CSS focus-within is wired as a second path', () => {
  const renderer = mount()
  assert.match(panelClass(renderer), /group-focus-within:opacity-100/)
  assert.match(panelClass(renderer), /group-hover:opacity-100/)

  act(() => {
    trigger(renderer).props.onClick({})
  })
  assert.match(panelClass(renderer), /\bopacity-100\b/, 'a mouse click opens it')

  // Wait out the touch guard so this second click counts as a mouse click.
  const realNow = Date.now
  Date.now = () => realNow() + 5000
  try {
    act(() => {
      trigger(renderer).props.onClick({})
    })
  } finally {
    Date.now = realNow
  }
  assert.match(panelClass(renderer), /\bopacity-0\b/, 'a second mouse click closes it')
})

test('Escape suppresses the panel and the next focus brings it back', () => {
  const renderer = mount()
  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'touch' })
  })
  act(() => {
    dispatch('keydown', { key: 'Escape' })
  })
  assert.equal(
    renderer.root.findByProps({ 'data-testid': 'board-counts' }).props['data-suppressed'],
    'true',
  )
  assert.match(panelClass(renderer), /\bopacity-0\b/)

  act(() => {
    renderer.root.findByProps({ 'data-testid': 'board-counts' }).props.onFocus()
  })
  assert.equal(
    renderer.root.findByProps({ 'data-testid': 'board-counts' }).props['data-suppressed'],
    undefined,
  )
})

test('five real rows and the sr-only snapshot line the acceptance scripts read', () => {
  const renderer = mount()
  const panel = renderer.root.findByProps({ 'data-testid': 'board-counts-panel' })
  // 面板是一段说明文字，不是对话框：读屏按 group 读，五个数字作为触发器的描述。
  assert.equal(panel.props.role, 'group')
  assert.equal(trigger(renderer).props['aria-haspopup'], undefined, 'it is not a popup')
  assert.equal(trigger(renderer).props['aria-describedby'], panel.props.id)
  const text = panel
    .findAllByType('span')
    .map((n) => n.props.children)
    .flat()
    .join(' ')
  assert.ok(text.includes('7,027'), 'evidence must come from board.evidence')
  assert.ok(text.includes('559/559 节点'), 'snapshot must come from loadedNodes/totalNodes')
  const srOnly = renderer.root
    .findAll((n) => n.type === 'span' && n.props.className === 'sr-only')
    .map((n) => n.props.children)
  assert.equal(srOnly.length, 1)
  assert.equal(srOnly[0][0], '快照已载 ')
})
