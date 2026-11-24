// The review switch lives in the project settings card as a real control, not a
// status line. It writes the project's own override through PUT /api/projects/{id}
// (the server applies it live when the project is attached), so it is not part of
// the draft/save flow. Closing is confirmed first because the server cancels live
// review runs; opening is safe to do directly.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

const uiStub = {
  name: 'stub-cyber-ui-review-switch',
  enforce: 'pre' as const,
  resolveId(id: string) {
    if (id === '@cyber/ui') return '\0stub-cyber-ui-review-switch'
    // Page.tsx (imported for Panel) pulls `cn` from the theme package; the real
    // one is source-only and cannot be resolved with configFile:false.
    if (id === '@cyber/theme') return '\0stub-cyber-theme'
    return null
  },
  load(id: string) {
    if (id === '\0stub-cyber-theme') {
      return "export const cn = (...args) => args.flat().filter(Boolean).join(' ')"
    }
    if (id !== '\0stub-cyber-ui-review-switch') return null
    return [
      "import React from 'react'",
      "export const Button = ({ children, variant, size, ...props }) => React.createElement('button', props, children)",
      "export const Badge = ({ children }) => React.createElement('span', null, children)",
      "export const Callout = ({ children }) => React.createElement('div', null, children)",
      "export const Field = ({ label, hint, error, children }) => React.createElement('div', null, React.createElement('span', null, label), children, error ? React.createElement('span', null, error) : hint ? React.createElement('span', null, hint) : null)",
      "export const Input = React.forwardRef((props, ref) => React.createElement('input', { ...props, ref }))",
      "export const Spinner = () => React.createElement('span', null, '…')",
      "export const Switch = (props) => React.createElement('button', { ...props, role: 'switch' })",
      "export const Dialog = ({ open, children }) => open ? React.createElement('div', { role: 'dialog' }, children) : null",
      "export const DialogContent = ({ children }) => React.createElement('div', null, children)",
      "export const DialogHeader = ({ children }) => React.createElement('div', null, children)",
      "export const DialogTitle = ({ children }) => React.createElement('h2', null, children)",
      "export const DialogDescription = ({ children }) => React.createElement('p', null, children)",
      "export const DialogFooter = ({ children }) => React.createElement('div', null, children)",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let ProjectSettingsCard: any, api: any
let renderer: ReactTestRenderer | undefined

// `reviews_disabled` is the value in force; `reviews_disabled_override` is the
// project's own stored value (null = follows the instance default).
const project = {
  id: 'p_one', name: '示例项目', created: '2026-11-24', intents: 0, facts: 0, evidence: 0, running: 0,
  max_runs_effective: 3,
  reviews_disabled: false,
  reviews_disabled_override: null as boolean | null,
}
const defaults = { max_runs: 3, round_timeout_seconds: 900, reviews_disabled_default: false }

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
  ProjectSettingsCard = (await server.ssrLoadModule('/src/components/ProjectSettingsCard.tsx')).ProjectSettingsCard
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => { await server?.close() })

async function render(p: Record<string, unknown> = project, d: Record<string, unknown> = defaults) {
  await act(async () => {
    renderer = create(React.createElement(ProjectSettingsCard, {
      project: p, defaults: d, onSaved() {}, onDirty() {},
    }))
  })
  return renderer!
}

const text = (r: ReactTestRenderer) => JSON.stringify(r.toJSON())
// Text nodes split by JSX interpolation are separate children; compare the
// concatenation when a sentence crosses one.
const plainText = (r: ReactTestRenderer): string => {
  const walk = (node: unknown): string => {
    if (node == null) return ''
    if (typeof node === 'string') return node
    if (Array.isArray(node)) return node.map(walk).join('')
    return walk((node as { children?: unknown }).children)
  }
  return walk(r.toJSON())
}
const switchNode = (r: ReactTestRenderer) => r.root.findByProps({ id: `reviews-${project.id}` })
const button = (r: ReactTestRenderer, label: string) =>
  r.root.findAllByType('button').find((b) => String(b.children.join('')) === label)!

test('closing asks first, because the server cancels live review runs', async () => {
  let writes = 0
  api.updateProject = async () => { writes++; return project }
  const r = await render()
  // The switch is a control now, not a status line: it starts checked and the
  // card no longer renders「复核已关闭」as static text.
  assert.equal(switchNode(r).props.checked, true)
  await act(async () => { switchNode(r).props.onCheckedChange(false) })
  assert.equal(writes, 0, 'the switch alone must not close reviews')
  assert.ok(text(r).includes('关闭复核？'), 'a confirmation dialog opens first')
})

test('confirming pins this project closed through the project update endpoint', async () => {
  const writes: unknown[][] = []
  api.updateProject = async (...args: unknown[]) => { writes.push(args); return { ...project, reviews_disabled: true, reviews_disabled_override: true } }
  const r = await render()
  await act(async () => { switchNode(r).props.onCheckedChange(false) })
  await act(async () => { button(r, '关闭复核').props.onClick() })
  assert.deepEqual(writes[0], ['p_one', { reviews_disabled: true }])
  assert.ok(text(r).includes('复核已关闭'), text(r))
  assert.ok(text(r).includes('正在跑的复核已取消'), 'the copy names what closing does to running reviews')
})

test('opening reviews needs no confirmation and pins the project open', async () => {
  const writes: unknown[][] = []
  api.updateProject = async (...args: unknown[]) => { writes.push(args); return { ...project, reviews_disabled: false, reviews_disabled_override: false } }
  const r = await render({ ...project, reviews_disabled: true })
  assert.equal(switchNode(r).props.checked, false)
  await act(async () => { switchNode(r).props.onCheckedChange(true) })
  assert.equal(writes.length, 1, 'opening applies directly, no dialog')
  assert.deepEqual(writes[0], ['p_one', { reviews_disabled: false }])
  assert.ok(text(r).includes('复核已开启'))
})

test('an override is labelled as one, and can be dropped to follow the instance again', async () => {
  const patches: unknown[][] = []
  api.updateProject = async (...args: unknown[]) => { patches.push(args); return { ...project, reviews_disabled: false, reviews_disabled_override: null } }
  const r = await render({ ...project, reviews_disabled: true, reviews_disabled_override: true })
  assert.ok(plainText(r).includes('项目复核 · 关闭'), text(r))
  assert.ok(plainText(r).includes('使用项目设置（实例默认开启）'), 'the card says the value does not follow the instance')
  const follow = button(r, '恢复跟随实例')
  assert.ok(follow, 'a pinned project can go back to inheriting')
  await act(async () => { follow.props.onClick() })
  assert.deepEqual(patches[0], ['p_one', { reviews_disabled: null }])
  assert.ok(text(r).includes('已恢复跟随实例'))
})

test('a project with no override names the instance default and offers no drop button', async () => {
  const r = await render()
  assert.ok(plainText(r).includes('跟随实例（实例默认开启）'), text(r))
  assert.ok(plainText(r).includes('项目复核 · 开启'), 'the effective value is spelled out separately')
  assert.equal(r.root.findByType('a').props.href, '/p/p_one/?view=docs', 'the link leaves global settings for this project’s documents')
  assert.ok(
    !r.root.findAllByType('button').some((b) => String(b.children.join('')) === '恢复跟随实例'),
    'nothing to drop when there is no override',
  )
})

test('a project attached before the default changed reports the switch it runs with', async () => {
  // No override, instance default is now "on", but this project attached while it
  // was "off" and never re-attached: the card must not claim the instance value
  // is already in force.
  const r = await render({ ...project, reviews_disabled: true, reviews_disabled_override: null })
  assert.ok(plainText(r).includes('跟随实例（实例默认开启）'), text(r))
  assert.ok(plainText(r).includes('项目复核 · 关闭'), text(r))
})

test('a failed close says so instead of pretending the switch moved', async () => {
  api.updateProject = async () => { throw new Error('保存复核开关失败：磁盘只读') }
  const r = await render()
  await act(async () => { switchNode(r).props.onCheckedChange(false) })
  await act(async () => { button(r, '关闭复核').props.onClick() })
  assert.ok(text(r).includes('关闭复核失败'), text(r))
  assert.ok(text(r).includes('磁盘只读'), text(r))
})
