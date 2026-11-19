// The stop dialog is the operator's only way to stop a direction: it must
// refuse an empty reason locally (the server would 409) and must show the
// server's 409 text instead of silently closing.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

// @cyber/ui's Dialog is a Radix portal and needs a DOM; stub the kit down to
// native elements and test this component's own validation/error state.
const uiStub = {
  name: 'stub-cyber-ui',
  enforce: 'pre' as const,
  resolveId(id: string) { return id === '@cyber/ui' ? '\0stub-cyber-ui' : null },
  load(id: string) {
    if (id !== '\0stub-cyber-ui') return null
    return [
      "import React from 'react'",
      "export const Button = ({ children, variant, size, ...props }) => React.createElement('button', props, children)",
      "export const Dialog = ({ open, children }) => open ? React.createElement('div', { role: 'dialog' }, children) : null",
      "export const DialogContent = ({ children }) => React.createElement('div', null, children)",
      "export const DialogHeader = ({ children }) => React.createElement('div', null, children)",
      "export const DialogTitle = ({ children }) => React.createElement('h2', null, children)",
      "export const DialogDescription = ({ children }) => React.createElement('p', null, children)",
      "export const DialogFooter = ({ children }) => React.createElement('div', null, children)",
      "export const Textarea = (props) => React.createElement('textarea', props)",
      "export const Callout = ({ children }) => React.createElement('div', null, children)",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let renderer: ReactTestRenderer | undefined
let StopIntentDialog: any, ApiError: any, api: any

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
  StopIntentDialog = (await server.ssrLoadModule('/src/components/StopIntentDialog.tsx')).StopIntentDialog
  const mod = await server.ssrLoadModule('/src/lib/api.ts')
  api = mod.api
  ApiError = mod.ApiError
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => {
  await server.close()
})

function textContent(node: any): string {
  if (node == null) return ''
  if (typeof node === 'string') return node
  if (Array.isArray(node)) return node.map(textContent).join('')
  if (typeof node === 'object') return textContent(node.children)
  return ''
}

function pageText(): string {
  return textContent(renderer!.toJSON())
}

function button(label: string) {
  const found = renderer!.root.findAll(
    (n) => n.type === 'button' && textContent(n.children).includes(label),
  )
  assert.ok(found.length > 0, `missing button ${label}`)
  return found[found.length - 1]
}

async function mount() {
  await act(async () => {
    renderer = create(
      React.createElement(StopIntentDialog, {
        target: { id: 'in_example', title: '核对账户访问边界' },
        onOpenChange() {},
        onStopped() {},
      }),
    )
  })
}

test('an empty reason never calls the API and explains why', async () => {
  let calls = 0
  api.cancelIntent = async () => { calls++; return { intent_id: 'in_example', run_id: 'r_1', held: true } }
  await mount()
  await act(async () => { button('确认停止').props.onClick() })
  assert.equal(calls, 0)
  assert.match(pageText(), /理由必填/)
})

test('a 409 from the server is shown with its status, not swallowed', async () => {
  api.cancelIntent = async () => {
    throw new ApiError(409, '意图 in_example 已经是 failed：用 retry 或 park，不要 cancel')
  }
  await mount()
  const textarea = renderer!.root.findByProps({ id: 'stop-intent-reason' })
  await act(async () => { textarea.props.onChange({ target: { value: '重复停止' } }) })
  await act(async () => { button('确认停止').props.onClick(); await Promise.resolve() })
  const text = pageText()
  assert.match(text, /HTTP 409/)
  assert.match(text, /用 retry 或 park/)
})
