import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

// FA2 (task-14) DOM-level tests for the data-dense pages: list semantics,
// accessible status regions, the deferred-rendering rule, and the modern filter
// field semantics. Styling itself is asserted by the source contract suite.
let server: Awaited<ReturnType<typeof createTestServer>>
let renderer: ReactTestRenderer | undefined
let Workers: any, Traffic: any, ConfirmProvider: any, api: any
const timers = new Map<number, () => void>()
let timerID = 0
const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const oldDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
const oldFormElement = Object.getOwnPropertyDescriptor(globalThis, 'HTMLFormElement')

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  Workers = (await server.ssrLoadModule('/src/views/Workers.tsx')).Workers
  Traffic = (await server.ssrLoadModule('/src/views/Traffic.tsx')).Traffic
  ConfirmProvider = (await server.ssrLoadModule('@cyber/ui')).ConfirmProvider
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: { pathname: '/p/alpha/', search: '', hash: '' },
      sessionStorage: { getItem: () => null, setItem: () => {} },
      setInterval: (fn: () => void) => {
        timers.set(++timerID, fn)
        return timerID
      },
      clearInterval: (id: number) => void timers.delete(id),
      setTimeout,
      clearTimeout,
      addEventListener() {},
      removeEventListener() {},
    },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: { visibilityState: 'visible', addEventListener() {}, removeEventListener() {} },
  })
  Object.defineProperty(globalThis, 'HTMLFormElement', { configurable: true, value: class {} })
})
afterEach(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
  timers.clear()
})
after(async () => {
  if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (oldDocument) Object.defineProperty(globalThis, 'document', oldDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  if (oldFormElement) Object.defineProperty(globalThis, 'HTMLFormElement', oldFormElement)
  else Reflect.deleteProperty(globalThis, 'HTMLFormElement')
  await server.close()
})

const worker = (i: number) => ({
  intent_id: `in_${i}`,
  participant_id: `pt_${i}`,
  title: `Direction ${i}`,
  status: 'running',
  running: true,
  can_kill: true,
  run_id: `r_${i}`,
  created_at: '2026-11-15T00:00:00Z',
})

async function tick() {
  await act(async () => {
    for (const fn of timers.values()) fn()
  })
}

test('the worker roster is a list with a counted live region, modern search field and deferred tail rows', async () => {
  api.workers = async () => ({ workers: Array.from({ length: 12 }, (_, i) => worker(i)), count: 12 })
  api.peers = async () => ({ mode: 'open', peer_policy: { mode: 'open', allow: [] } })
  api.participants = async () => []
  await act(async () => {
    renderer = create(React.createElement(ConfirmProvider, null, React.createElement(Workers, { current: 'alpha', projects: [], onRefresh() {} })))
  })
  await tick()

  const list = renderer!.root.findAllByType('ul').find((node) => node.props['aria-label'] === '执行者名单')
  assert.ok(list, 'roster must be an accessible list')
  const rows = list.findAllByType('li')
  assert.equal(rows.length, 12, 'every worker row is a list item')
  // Guide `defer-rendering-heavy-content`: the first viewport stays uncontained,
  // only the rows below it are skipped.
  assert.ok(!String(rows[0].props.className ?? '').includes('content-visibility'), 'first rows must not defer')
  assert.match(String(rows[6].props.className), /\[content-visibility:auto\]/, 'tail rows must defer rendering')
  assert.match(String(rows[6].props.className), /\[contain-intrinsic-size:auto_/, 'deferred rows need a placeholder size')

  const status = renderer!.root.findAllByType('p').find((node) => node.props.role === 'status')
  assert.ok(status, 'filter results need a live status region')
  assert.ok(JSON.stringify(status.children).includes('12'), 'status must announce the visible count')

  const input = renderer!.root.findByType('input')
  assert.equal(input.props.type, 'search')
  assert.equal(input.props.inputMode, 'search')
  assert.equal(input.props.enterKeyHint, 'search')
  assert.equal(input.props.autoComplete, 'off')
  assert.equal(input.props.spellCheck, false)
})

const flow = (i: number) => ({
  ref: `f_${i}`,
  method: 'GET',
  status: 200,
  at: '2026-11-15T00:00:00Z',
  url: `https://target.test/${i}`,
  run_id: `r_${i}`,
  response_bytes: 10 + i,
  complete: true,
})

test('the traffic list is a named list with pagination announced, and only tail rows defer', async () => {
  api.httpFlows = async () => ({ flows: Array.from({ length: 12 }, (_, i) => flow(i)), total: 12 })
  api.httpFlow = async (ref: string) => ({
    record: { ref },
    flow: { request: { method: 'GET', url: 'https://target.test/', protocol: 'HTTP/1.1', headers: [], body: '' }, response: null },
  })
  await act(async () => {
    renderer = create(React.createElement(Traffic))
  })
  await tick()

  const list = renderer!.root.findAllByType('ul').find((node) => node.props['aria-label'] === '流量记录')
  assert.ok(list, 'traffic rows must be an accessible list')
  const rows = list.findAllByType('li')
  assert.equal(rows.length, 12)
  assert.ok(!String(rows[0].props.className ?? '').includes('content-visibility'), 'first traffic rows must not defer')
  assert.match(String(rows[8].props.className), /\[content-visibility:auto\]/)

  const status = renderer!.root.findAllByType('span').find((node) => node.props.role === 'status')
  assert.ok(status, 'pagination needs a live status region')
  assert.equal(status.props['aria-live'], 'polite')
  const statusText = [status.children].flat(Infinity).join('')
  assert.ok(statusText.includes('共 12 条'), `pagination status must state the total, got: ${statusText}`)

  const current = renderer!.root.findAllByType('button').find((node) => node.props['aria-current'] === 'true')
  assert.ok(current, 'the selected flow is exposed with aria-current')
})
