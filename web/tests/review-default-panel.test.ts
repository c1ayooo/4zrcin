// The instance-level review default lives on the runtime-settings endpoint, next
// to the auxiliary gate. It is one level with the launch flag (--no-verify seeds
// it, the saved value wins), and per-project overrides still take precedence.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

const uiStub = {
  name: 'stub-cyber-ui-review-default',
  enforce: 'pre' as const,
  resolveId(id: string) {
    if (id === '@cyber/ui') return '\0stub-cyber-ui-review-default'
    if (id === '@cyber/theme') return '\0stub-cyber-theme'
    return null
  },
  load(id: string) {
    if (id === '\0stub-cyber-theme') return "export const cn = (...args) => args.flat().filter(Boolean).join(' ')"
    if (id !== '\0stub-cyber-ui-review-default') return null
    return [
      "import React from 'react'",
      "export const Badge = ({ children }) => React.createElement('span', null, children)",
      "export const Button = ({ children, ...props }) => React.createElement('button', props, children)",
      "export const Callout = ({ children }) => React.createElement('div', null, children)",
      "export const Switch = (props) => React.createElement('button', { ...props, role: 'switch' })",
      "export const Input = React.forwardRef((props, ref) => React.createElement('input', { ...props, ref }))",
      "export const Textarea = React.forwardRef((props, ref) => React.createElement('textarea', { ...props, ref }))",
      "export const Spinner = () => React.createElement('span', null, '…')",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let ReviewDefaultPanel: any, api: any
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [react(), uiStub],
    esbuild: false,
    resolve: {
      alias: [{ find: '@', replacement: fileURLToPath(new URL('../src', import.meta.url)) }],
      dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime'],
    },
  })
  ReviewDefaultPanel = (await server.ssrLoadModule('/src/views/Settings.tsx')).ReviewDefaultPanel
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => { await server?.close() })

type Runtime = { reviews_disabled?: boolean }

async function render(data: Runtime, error?: string) {
  const runtime = {
    data: Object.keys(data).length ? { auxiliary_concurrency: 1, max_auxiliary_concurrency: 8, ...data } : undefined,
    loading: false, error, reload() {}, setData() {},
  }
  await act(async () => {
    renderer = create(React.createElement(ReviewDefaultPanel, { runtime }))
  })
  return renderer!
}

const text = (r: ReactTestRenderer) => JSON.stringify(r.toJSON())
const switchNode = (r: ReactTestRenderer) => r.root.findByProps({ id: 'review-default' })

test('the panel reads the instance default and calls it 实例级', async () => {
  const r = await render({ reviews_disabled: false, reviews_disabled_stored: true })
  assert.ok(text(r).includes('实例级'))
  // The label is composed from two children, so match the parts.
  assert.ok(text(r).includes('新项目的复核默认值：'), text(r))
  assert.ok(text(r).includes('"开启"'), text(r))
  assert.equal(switchNode(r).props.checked, true)
})

test('turning it off saves the instance default on the runtime-settings endpoint', async () => {
  const saved: unknown[] = []
  api.saveRuntimeSettings = async (body: unknown) => { saved.push(body); return { auxiliary_concurrency: 1, max_auxiliary_concurrency: 8, reviews_disabled: true } }
  const r = await render({ reviews_disabled: false })
  await act(async () => { switchNode(r).props.onCheckedChange(false) })
  assert.deepEqual(saved[0], { reviews_disabled: true })
  assert.ok(text(r).includes('默认关闭复核'), text(r))
  assert.ok(text(r).includes('已单独覆盖开启的项目不受影响'), 'an explicit project override is untouched')
})

test('the switch shows the effective default without explaining where it came from', async () => {
  const r = await render({ reviews_disabled: false })
  assert.ok(!text(r).includes('启动参数'), 'the value in effect read is all the operator needs')
  assert.ok(!text(r).includes('还没有保存过'))
})

test('the panel says the default only lands when a project attaches', async () => {
  const r = await render({ reviews_disabled: false })
  assert.ok(text(r).includes('改这里不会切换正在运行的项目'), text(r))
})

test('a missing field says it is loading instead of pretending reviews are on', async () => {
  const r = await render({})
  assert.equal(switchNode(r).props.checked, false, 'unknown must not read as "on"')
  assert.equal(switchNode(r).props.disabled, true, 'no guessing: the switch is disabled until the value is known')
  assert.ok(text(r).includes('读取中…'), text(r))
})
