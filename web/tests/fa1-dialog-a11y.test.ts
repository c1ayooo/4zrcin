// Dialog form behaviour that must not depend on Radix being mounted: required
// semantics, aria-invalid + error description, submit-attempt validation, and
// the focus policy for a costly confirmation. The kit is stubbed down to native
// elements (the real Dialog needs a DOM portal).
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

const uiStub = {
  name: 'stub-cyber-ui-dialogs',
  enforce: 'pre' as const,
  resolveId(id: string) { return id === '@cyber/ui' ? '\0stub-cyber-ui-dialogs' : null },
  load(id: string) {
    if (id !== '\0stub-cyber-ui-dialogs') return null
    return [
      "import React from 'react'",
      "const without = (props, keys) => { const out = { ...props }; for (const k of keys) delete out[k]; return out }",
      "export const Button = ({ children, variant, size, active, ...props }) => React.createElement('button', props, children)",
      "const DIALOG_ONLY = ['onCloseAutoFocus', 'onEscapeKeyDown', 'onPointerDownOutside', 'onInteractOutside', 'asChild', 'forceMount']",
      "export const Dialog = ({ open, children }) => open ? React.createElement('div', { role: 'dialog' }, children) : null",
      "export const DialogContent = ({ children, ...props }) => React.createElement('div', without(props, DIALOG_ONLY), children)",
      "export const DialogHeader = ({ children }) => React.createElement('div', null, children)",
      "export const DialogTitle = ({ children }) => React.createElement('h2', null, children)",
      "export const DialogDescription = ({ children }) => React.createElement('p', null, children)",
      "export const DialogFooter = ({ children }) => React.createElement('div', null, children)",
      "export const Field = ({ label, hint, error, children }) => React.createElement('div', null, React.createElement('span', null, label), children, error ? React.createElement('span', null, error) : hint ? React.createElement('span', null, hint) : null)",
      "export const Input = React.forwardRef((props, ref) => React.createElement('input', { ...props, ref }))",
      "export const Textarea = React.forwardRef((props, ref) => React.createElement('textarea', { ...props, ref }))",
      "export const Callout = ({ children }) => React.createElement('div', null, children)",
      "export const Spinner = () => React.createElement('span', null, '…')",
      "export const Switch = (props) => React.createElement('button', { ...props, role: 'switch' })",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let NewProjectDialog: any, CreateGoalDialog: any, SealGoalDialog: any, AppendGoalDialog: any, api: any
let renderer: ReactTestRenderer | undefined
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
let assigned: string[] = []
let focused: string[] = []

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
  NewProjectDialog = (await server.ssrLoadModule('/src/components/NewProjectDialog.tsx')).NewProjectDialog
  CreateGoalDialog = (await server.ssrLoadModule('/src/components/CreateGoalDialog.tsx')).CreateGoalDialog
  SealGoalDialog = (await server.ssrLoadModule('/src/components/SealGoalDialog.tsx')).SealGoalDialog
  AppendGoalDialog = (await server.ssrLoadModule('/src/components/AppendGoalDialog.tsx')).AppendGoalDialog
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  const storage = new Map<string, string>()
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: { pathname: '/p/p1/', search: '', hash: '', assign: (url: string) => { assigned.push(url) } },
      sessionStorage: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) },
      addEventListener() {},
      removeEventListener() {},
      setInterval,
      clearInterval,
    },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      getElementById: (id: string) => id === 'seal-goal-close' ? { focus: () => focused.push(id) } : null,
      addEventListener() {},
      removeEventListener() {},
    },
  })
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
  assigned = []
  focused = []
})

after(async () => {
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  await server?.close()
})

function text(): string {
  return JSON.stringify(renderer!.toJSON())
}
function button(label: string) {
  return renderer!.root.findAllByType('button').find((node) => String(node.children.join('')) === label)!
}
async function click(label: string) {
  await act(async () => { button(label).props.onClick() })
}

test('the new-project form validates on submit, marks the field and links its error', async () => {
  const changes: boolean[] = []
  api.createProject = async () => ({ id: 'p_new' })
  await act(async () => {
    renderer = create(React.createElement(NewProjectDialog, { open: true, onOpenChange: (open: boolean) => changes.push(open) }))
  })
  const name = renderer!.root.findByProps({ 'aria-label': '项目名称' })
  assert.equal(name.props.required, true)
  await click('创建并开始')
  assert.ok(text().includes('项目名称不能为空'))
  assert.equal(renderer!.root.findByProps({ 'aria-label': '项目名称' }).props['aria-invalid'], true)
  assert.equal(renderer!.root.findByProps({ 'aria-label': '项目名称' }).props['aria-describedby'], 'new-project-name-error')

  await act(async () => { renderer!.root.findByProps({ 'aria-label': '项目名称' }).props.onChange({ target: { value: ' NX 门户 ' } }) })
  await click('创建并开始')
  const textareas = renderer!.root.findAllByType('textarea')
  assert.equal(textareas[0].props['aria-invalid'], true, 'the start field is the next required field')
  assert.ok(!text().includes('项目名称不能为空'), 'the fixed field stops showing its error')
})

test('a complete new-project form submits trimmed values and closes on success', async () => {
  const changes: boolean[] = []
  const created: unknown[][] = []
  api.createProject = async (...args: unknown[]) => { created.push(args); return { id: 'p_new' } }
  await act(async () => {
    renderer = create(React.createElement(NewProjectDialog, { open: true, onOpenChange: (open: boolean) => changes.push(open) }))
  })
  await act(async () => { renderer!.root.findByProps({ 'aria-label': '项目名称' }).props.onChange({ target: { value: ' NX 门户 ' } }) })
  const textareas = renderer!.root.findAllByType('textarea')
  await act(async () => { textareas[0].props.onChange({ target: { value: ' 门户登录口 ' } }) })
  await act(async () => { textareas[1].props.onChange({ target: { value: ' 拿到越权证据 ' } }) })
  await click('创建并开始')
  assert.equal(created.length, 1)
  assert.deepEqual(created[0].slice(0, 3), ['NX 门户', '门户登录口', '拿到越权证据'])
  assert.deepEqual(changes, [false])
  assert.ok(assigned[0].startsWith('/p/p_new/'))
})

test('a goal dialog refuses an empty end state and marks the textarea', async () => {
  let calls = 0
  api.createGoal = async () => { calls++; return {} }
  await act(async () => {
    renderer = create(React.createElement(CreateGoalDialog, { open: true, onOpenChange() {}, onCreated() {} }))
  })
  await click('创建')
  assert.equal(calls, 0)
  const statement = renderer!.root.findAllByType('textarea')[1]
  assert.equal(statement.props.required, true)
  assert.equal(statement.props['aria-invalid'], true)
  assert.equal(statement.props['aria-describedby'], 'create-goal-statement-error')
  assert.ok(text().includes('终点不能为空'))
})

test('the CTF preset fills the two drafts and never submits', async () => {
  const presets = (await server.ssrLoadModule('/src/lib/goal-presets.ts')).GOAL_PRESETS as Array<{
    id: string; start: string; statement: string
  }>
  const ctf = presets.find((preset) => preset.id === 'ctf-flags')
  assert.ok(ctf, 'the CTF shape is built in')
  let calls = 0
  api.createGoal = async () => { calls++; return {} }
  const closed: boolean[] = []
  await act(async () => {
    renderer = create(React.createElement(CreateGoalDialog, {
      open: true, onOpenChange: (v: boolean) => closed.push(v), onCreated() {},
    }))
  })
  const toggle = renderer!.root.findAll((node) => node.type === 'button' && node.props?.['aria-expanded'] !== undefined)[0]
  await act(async () => { toggle.props.onClick() })
  const item = renderer!.root.findByProps({ 'data-goal-preset': 'ctf-flags' })
  await act(async () => { item.props.onClick() })
  // The pick writes 起点/终点 and stops: the dialog stays open for editing, and
  // no goal is created until the operator presses 创建.
  const [start, statement] = renderer!.root.findAllByType('textarea').map((area) => area.props.value)
  assert.equal(start, ctf.start)
  assert.equal(statement, ctf.statement)
  assert.equal(calls, 0, 'picking a preset never creates a goal')
  assert.deepEqual(closed, [], 'the dialog stays open so the text can be edited')
})

test('a costly closeout starts the focus on the safe action, not on the model call', async () => {
  await act(async () => {
    renderer = create(React.createElement(SealGoalDialog, {
      goal: { id: 'g1', statement: '拿到域管' }, open: true, onOpenChange() {},
    }))
  })
  const content = renderer!.root.findAll((node) => typeof node.type === 'string' && Boolean(node.props?.onOpenAutoFocus))
    .find((node) => node.props.onOpenAutoFocus)!
  let prevented = false
  await act(async () => { content.props.onOpenAutoFocus({ preventDefault: () => { prevented = true } }) })
  assert.equal(prevented, true)
  assert.deepEqual(focused, ['seal-goal-close'])
})

test('an append-goal submit without an end state points the textarea at its error', async () => {
  await act(async () => {
    renderer = create(React.createElement(AppendGoalDialog, {
      open: true, onOpenChange() {}, paused: true, projectID: 'p1', onCompleted() {},
    }))
  })
  await click('追加目标并继续')
  const statement = renderer!.root.findAllByType('textarea')[1]
  assert.equal(statement.props['aria-invalid'], true)
  assert.equal(statement.props['aria-describedby'], 'append-goal-error')
  assert.ok(text().includes('终点不能为空'))
})
