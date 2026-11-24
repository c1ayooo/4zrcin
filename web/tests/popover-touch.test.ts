// 「视图 / 更多 / 更多通信工具 / 状态说明」四个 Popover 入口的触摸路径。
//
// 用户环境是 Firefox + 触屏：一次 tap 可能不送 `click`，也可能送两次（被当成双击）。
// Radix 的 Trigger 只在 click 上开合，所以在库里统一兜住——按下即开合，随后的 click
// 忽略。这里把 `@radix-ui/react-popover` 换成一个小桩，按它真实的组合规则实现：
// `onClick: composeEventHandlers(props.onClick, onOpenToggle)`，内部开合只在
// `event.defaultPrevented === false` 时执行（dist/index.mjs:110 同一语义）。
import { before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

const radixStub = {
  name: 'stub-radix-popover',
  enforce: 'pre' as const,
  resolveId(id: string) {
    return id === '@radix-ui/react-popover' ? '\0stub-radix-popover' : null
  },
  load(id: string) {
    if (id !== '\0stub-radix-popover') return null
    return [
      "import React from 'react'",
      'const Ctx = React.createContext(null)',
      'export const Root = ({ open, onOpenChange, children }) =>',
      "  React.createElement(Ctx.Provider, { value: { open, onOpenChange } },",
      "    React.createElement('div', { 'data-open': String(open) }, children))",
      'export const Trigger = React.forwardRef(({ children, ...props }, ref) => {',
      '  const ctx = React.useContext(Ctx)',
      "  return React.createElement('button', { ref, ...props, 'aria-expanded': ctx ? String(ctx.open) : undefined,",
      '    onClick: (event) => {',
      '      props.onClick?.(event)',
      '      if (!event.defaultPrevented) ctx?.onOpenChange(!ctx.open)',
      '    } })',
      '})',
      'Trigger.displayName = "Trigger"',
      "export const Content = ({ children }) => React.createElement('div', { role: 'dialog' }, children)",
      'export const Portal = ({ children }) => children',
      "export const Close = ({ children }) => React.createElement('button', null, children)",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let Popover: any, PopoverTrigger: any, PopoverContent: any

before(async () => {
  server = await createTestServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [react(), radixStub],
    esbuild: false,
    // 默认 SSR 会把 node_modules 里的依赖 externalize 掉——那样就绕过上面的桩、
    // 直接 require 真的 Radix（它要 DOM portal）。这里只把这个包拉回图里处理。
    ssr: { noExternal: ['@radix-ui/react-popover'] },
    resolve: {
      alias: [
        { find: '@cyber/theme', replacement: fileURLToPath(new URL('../packages/theme/src', import.meta.url)) },
        { find: '@', replacement: fileURLToPath(new URL('../src', import.meta.url)) },
      ],
      dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime'],
    },
  })
  const mod = await server.ssrLoadModule('/packages/ui/src/popover.tsx')
  Popover = mod.Popover
  PopoverTrigger = mod.PopoverTrigger
  PopoverContent = mod.PopoverContent
})

function mount(): ReactTestRenderer {
  let renderer: ReactTestRenderer | undefined
  act(() => {
    renderer = create(
      React.createElement(
        Popover,
        null,
        React.createElement(PopoverTrigger, { 'data-testid': 'trigger' }, '视图'),
        React.createElement(PopoverContent, null, '面板'),
      ),
    )
  })
  assert.ok(renderer)
  return renderer
}

// 触发器最终渲染成库里的 <button>：`onPointerDown` 是这一层加上的，所以断言看它，
// 而不是包装组件自己的 props。
const trigger = (r: ReactTestRenderer) => r.root.findByType('button')
const expanded = (r: ReactTestRenderer) => trigger(r).props['aria-expanded']
/** Radix 的 click 事件形状：只有被 `preventDefault()` 过，内部开合才不执行。 */
function clickEvent() {
  return {
    defaultPrevented: false,
    preventDefault(this: { defaultPrevented: boolean }) {
      this.defaultPrevented = true
    },
  }
}

test('a touch press opens the panel, and the clicks that follow do not close it', () => {
  const renderer = mount()
  assert.equal(expanded(renderer), 'false')

  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'touch' })
  })
  assert.equal(expanded(renderer), 'true', 'the touch press must open the panel')

  // Firefox 的 tap 可能补一到两次 click：都被忽略，不能被取反回去。
  const first = clickEvent()
  act(() => {
    trigger(renderer).props.onClick(first)
  })
  assert.equal(first.defaultPrevented, true, 'the trailing click must be swallowed for Radix too')
  act(() => {
    trigger(renderer).props.onClick(clickEvent())
  })
  assert.equal(expanded(renderer), 'true', 'trailing clicks must not toggle it back')
})

test('a second tap closes it, and a mouse click still toggles', () => {
  const renderer = mount()
  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'touch' })
  })
  assert.equal(expanded(renderer), 'true')

  // 一次 tap 只来一次 pointerdown，所以第二次按下就是「关」。
  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'touch' })
  })
  assert.equal(expanded(renderer), 'false', 'a second tap closes it')

  // 触屏防护过了 700ms 之后，鼠标点击走 Radix 原路（开）。
  const realNow = Date.now
  Date.now = () => realNow() + 5000
  try {
    act(() => {
      trigger(renderer).props.onClick(clickEvent())
    })
  } finally {
    Date.now = realNow
  }
  assert.equal(expanded(renderer), 'true', 'a mouse click opens it again')
})

test('a mouse pointerdown does not pre-open the panel', () => {
  const renderer = mount()
  act(() => {
    trigger(renderer).props.onPointerDown({ pointerType: 'mouse', button: 0 })
  })
  assert.equal(expanded(renderer), 'false', 'only touch presses open on pointerdown')
})
