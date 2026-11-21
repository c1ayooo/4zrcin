import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import react from '@vitejs/plugin-react-swc'
import { createTestServer } from './helpers/vite.ts'

// @cyber/ui's Dialog is a Radix portal: the real one needs a DOM. The goal editor
// only uses plain form primitives, so the test stubs the kit down to native
// elements and exercises the component's own state/validation logic. The server
// is configured from scratch (configFile: false) so the stub wins over the app's
// real @cyber/ui alias. `createTestServer` keeps the dependency optimizer off, so
// the SWC transform runs without esbuild ever writing a cache file.
const uiStub = {
  name: 'stub-cyber-ui',
  enforce: 'pre' as const,
  resolveId(id: string) { return id === '@cyber/ui' ? '\0stub-cyber-ui' : null },
  load(id: string) {
    if (id !== '\0stub-cyber-ui') return null
    return [
      "import React from 'react'",
      "const without = (props, keys) => { const out = { ...props }; for (const k of keys) delete out[k]; return out }",
      "export const Button = ({ children, variant, size, ...props }) => React.createElement('button', props, children)",
      "export const Dialog = ({ open, children }) => open ? React.createElement('div', { role: 'dialog' }, children) : null",
      "export const DialogContent = ({ children }) => React.createElement('div', null, children)",
      "export const DialogHeader = ({ children }) => React.createElement('div', null, children)",
      "export const DialogTitle = ({ children }) => React.createElement('h2', null, children)",
      "export const DialogDescription = ({ children }) => React.createElement('p', null, children)",
      "export const Field = ({ label, hint, error, children }) => React.createElement('div', null, React.createElement('span', null, label), hint ? React.createElement('span', null, hint) : null, error ? React.createElement('span', null, error) : null, children)",
      "export const Input = (props) => React.createElement('input', without(props, ['aria-label']))",
      "export const Textarea = (props) => React.createElement('textarea', props)",
      "export const Callout = ({ children }) => React.createElement('div', null, children)",
    ].join('\n')
  },
}

let server: Awaited<ReturnType<typeof createTestServer>>
let GoalEditorDialog: React.ComponentType<any>
let api: any
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
  GoalEditorDialog = (await server.ssrLoadModule('/src/components/GoalEditorDialog.tsx')).GoalEditorDialog
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})
afterEach(async () => { await act(async () => { renderer?.unmount(); renderer = undefined }) })
after(async () => { await server?.close() })

function goal(over: Record<string, unknown> = {}) {
  return { id: 'g_1', statement: '拿到域管', start: '从 VPN 入口开始', met: false, created_at: '2026-11-17T00:00:00Z', revision: 7, ...over }
}
function button(label: string) {
  return renderer!.root.findAllByType('button').find((node) => node.children.join('') === label)!
}

test('saving an edit sends the operator-written start and statement through updateGoal', async () => {
  const updates: unknown[][] = []
  const events: string[] = []
  api.updateGoal = async (...args: unknown[]) => { updates.push(args); return goal() }
  api.closeGoal = async () => goal({ closed: true })
  await act(async () => {
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal(), onClose: () => events.push('close'), onSaved: () => events.push('saved'),
    }))
  })
  await act(async () => { button('保存修改').props.onClick() })
  assert.deepEqual(updates, [['g_1', '拿到域管', '从 VPN 入口开始', 7]], 'the statement is the goal document: the revision has to go back with it')
  assert.deepEqual(events, ['saved', 'close'])
})

test('a goal row without its own revision reads it from the goal document', async () => {
  const updates: unknown[][] = []
  api.updateGoal = async (...args: unknown[]) => { updates.push(args); return goal() }
  api.documents = async () => ({
    documents: {
      goal: { text: '拿到域管', revision: 12, updated_at: '2026-11-21T00:00:00Z', updated_by: 'op', goal_id: 'g_1' },
      scope: { text: '', revision: 1, updated_at: '', updated_by: '' },
      constraints: { text: '', revision: 1, updated_at: '', updated_by: '' },
    },
  })
  await act(async () => {
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal({ revision: undefined }), onClose: () => {}, onSaved: () => {},
    }))
  })
  await act(async () => { button('保存修改').props.onClick() })
  assert.deepEqual(updates, [['g_1', '拿到域管', '从 VPN 入口开始', 12]])
})

test('without a revision nothing is saved, so a stale edit cannot overwrite a newer one', async () => {
  const updates: unknown[][] = []
  let fail = true
  api.updateGoal = async (...args: unknown[]) => { updates.push(args); return goal() }
  api.documents = async () => { if (fail) throw new Error('documents unavailable'); return { documents: {} } }
  await act(async () => {
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal({ revision: undefined }), onClose: () => {}, onSaved: () => {},
    }))
  })
  assert.equal(button('保存修改').props.disabled, true, 'a save without a revision must not be offered')
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('读不到这条目标当前的修订号'))
  await act(async () => { button('保存修改').props.onClick() })
  fail = false
  // A different goal's document must not lend its revision to this one.
  api.documents = async () => ({ documents: { goal: { text: '别处的目标', revision: 3, updated_at: '', updated_by: '', goal_id: 'g_2' } } })
  await act(async () => {
    renderer!.unmount()
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal({ revision: undefined }), onClose: () => {}, onSaved: () => {},
    }))
  })
  assert.equal(button('保存修改').props.disabled, true)
})

test('closing requires a reason and keeps the dialog open on a server refusal', async () => {
  const closes: unknown[][] = []
  const events: string[] = []
  let fail = true
  api.updateGoal = async () => goal()
  api.closeGoal = async (...args: unknown[]) => { closes.push(args); if (fail) throw new Error('目标已关闭'); return goal({ closed: true, closed_reason: '操作员撤回' }) }
  await act(async () => {
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal(), onClose: () => events.push('close'), onSaved: () => events.push('saved'),
    }))
  })
  assert.equal(button('关闭目标').props.disabled, true, 'an empty reason must not submit')
  await act(async () => { renderer!.root.findByProps({ placeholder: '说明为什么撤回这个目标' }).props.onChange({ target: { value: '操作员撤回' } }) })
  assert.equal(button('关闭目标').props.disabled, false)

  await act(async () => { button('关闭目标').props.onClick() })
  assert.deepEqual(closes, [['g_1', '操作员撤回']])
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('目标已关闭'), 'the server refusal must be visible')
  assert.deepEqual(events, [], 'a failed close must not close the dialog')

  fail = false
  await act(async () => { button('关闭目标').props.onClick() })
  assert.deepEqual(events, ['saved', 'close'])
})

test('a closed goal is read-only and shows why it was withdrawn', async () => {
  await act(async () => {
    renderer = create(React.createElement(GoalEditorDialog, {
      goal: goal({ closed: true, closed_reason: '口径已被取代' }), onClose: () => {}, onSaved: () => {},
    }))
  })
  assert.equal(button('保存修改'), undefined)
  const text = JSON.stringify(renderer!.toJSON())
  assert.ok(text.includes('已关闭：'))
  assert.ok(text.includes('口径已被取代'))
})
