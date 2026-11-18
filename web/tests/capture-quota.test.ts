import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import { captureQuotaBadge, captureQuotaExceeded, captureQuotaNote } from '../src/lib/traffic.ts'

// ---- pure rules --------------------------------------------------------------

test('quota_exceeded is a project budget state, never a per-capture error', () => {
  assert.equal(captureQuotaExceeded({ quota_exceeded: true }), true)
  assert.equal(captureQuotaExceeded({ quota_exceeded: false }), false)
  // Legacy captures/index rows predate the field: absent means "no signal",
  // so the console must not show the badge for them.
  assert.equal(captureQuotaExceeded({}), false)
  assert.match(captureQuotaNote, /项目级正文预算（默认 2 GiB）用尽/)
  assert.match(captureQuotaNote, /只保留方法、URL、状态码、字节数等元数据/)
  assert.match(captureQuotaNote, /超限不会删除已有证据或旧正文/)
  assert.ok(captureQuotaBadge.includes('项目正文配额已满（仅元数据）'))
})

// ---- render: the traffic detail surfaces the quota state ---------------------

let server: Awaited<ReturnType<typeof createTestServer>>
let Traffic: any
let api: any
let renderer: ReactTestRenderer | undefined
const timers = new Map<number, () => void>()
let timerID = 0
const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const oldDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
const oldFormElement = Object.getOwnPropertyDescriptor(globalThis, 'HTMLFormElement')

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  Traffic = (await server.ssrLoadModule('/src/views/Traffic.tsx')).Traffic
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: { pathname: '/p/alpha/', search: '', hash: '' },
      sessionStorage: { getItem: () => null, setItem: () => {} },
      setInterval: (fn: () => void) => { timers.set(++timerID, fn); return timerID },
      clearInterval: (id: number) => void timers.delete(id),
      setTimeout, clearTimeout,
      addEventListener() {}, removeEventListener() {},
    },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: { visibilityState: 'visible', addEventListener() {}, removeEventListener() {} },
  })
  Object.defineProperty(globalThis, 'HTMLFormElement', { configurable: true, value: class {} })
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
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

function mockFlow(quotaExceeded: boolean | undefined) {
  const row = {
    ref: 'r_1/f_1', run_id: 'r_1', intent_id: 'in_1', event_id: 'ev_1', flow_id: 'f_1',
    method: 'GET', url: 'https://target.test/', status: 200, at: '2026-11-15T00:00:00Z',
    complete: true, request_bytes: 10, response_bytes: 20,
    ...(quotaExceeded === undefined ? {} : { quota_exceeded: quotaExceeded }),
  }
  api.httpFlows = async () => ({ flows: [row], total: 1 })
  api.httpFlow = async () => ({
    record: row,
    flow: { request: { method: 'GET', url: 'https://target.test/', protocol: 'HTTP/1.1', headers: [], body: '' }, response: null },
  })
}

async function renderTraffic(quotaExceeded: boolean | undefined) {
  mockFlow(quotaExceeded)
  await act(async () => {
    renderer = create(React.createElement(Traffic))
  })
  await act(async () => {
    for (const fn of timers.values()) fn()
  })
  return JSON.stringify(renderer!.toJSON())
}

test('the traffic detail shows the quota badge and the 2 GiB retention rule', async () => {
  const text = await renderTraffic(true)
  assert.ok(text.includes('项目正文配额已满（仅元数据）'), text)
  assert.ok(text.includes('2 GiB'), text)
  assert.ok(text.includes('不会删除已有证据'), text)
  assert.ok(text.includes('仅元数据（项目正文配额已满，未保存正文）'), text)
})

test('a legacy row without quota_exceeded shows no quota badge', async () => {
  const absent = await renderTraffic(undefined)
  assert.ok(!absent.includes('项目正文配额已满'), absent)
  const explicitFalse = await renderTraffic(false)
  assert.ok(!explicitFalse.includes('项目正文配额已满'), explicitFalse)
})
