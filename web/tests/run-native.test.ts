import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  appendRunHistory,
  commandDetailText,
  commandMatches,
  historyEventSummary,
  nativeSessionErrorText,
  nextRunHistoryCursor,
} from '../src/lib/run-native.ts'
import type { NativeSessionEvent, RunHistoryPage } from '../src/lib/types.ts'

// ---- pure helpers ------------------------------------------------------------

test('history summaries read the recognisable fields and fall back to raw JSON', () => {
  assert.deepEqual(historyEventSummary({ kind: 'status', state: 'eval_end', text: 'eval_end 第2/3轮：通过', at: '2026-11-18T00:00:00Z' }), {
    kind: 'status',
    state: 'eval_end',
    at: '2026-11-18T00:00:00Z',
    text: 'eval_end 第2/3轮：通过',
  })
  assert.equal(historyEventSummary({ payload: { kind: 'message', message: { text: '你好' } } }).text, '你好')
  const unknown = historyEventSummary({ opaque: { nested: 1 } })
  assert.equal(unknown.kind, 'event')
  assert.match(unknown.text, /opaque/)
})

test('paging appends by cursor, never duplicates a retried page, and reports the honest end', () => {
  const first: RunHistoryPage = {
    run_id: 'r1', session_id: 's1', source: 'aop.ListEvents', has_more: true, next_cursor: 'c2',
    events: [{ cursor: 'c1', event: { kind: 'status' } }],
  }
  const second: RunHistoryPage = {
    run_id: 'r1', session_id: 's1', source: 'aop.ListEvents', has_more: false,
    events: [{ cursor: 'c2', event: { kind: 'message' } }],
  }
  const merged = appendRunHistory(appendRunHistory([], first), second)
  assert.deepEqual(merged.map((event) => event.cursor), ['c1', 'c2'])
  // A lost response retried with the same cursor must not duplicate rows.
  assert.deepEqual(appendRunHistory(merged, second).map((event) => event.cursor), ['c1', 'c2'])
  assert.equal(nextRunHistoryCursor(first), 'c2')
  assert.equal(nextRunHistoryCursor(second), '')
  assert.equal(nextRunHistoryCursor(undefined), '')
})

test('native read errors explain no-session/host-down instead of pretending failure', () => {
  assert.equal(nativeSessionErrorText({ status: 409, code: 'no_native_session', message: '旧运行' }).tone, 'info')
  assert.match(nativeSessionErrorText({ status: 409, message: '旧运行' }).text, /没有原生 AOP 会话/)
  assert.equal(nativeSessionErrorText({ status: 503, code: 'host_unavailable', message: '宿主离线' }).tone, 'info')
  const read = nativeSessionErrorText({ status: 502, message: '宿主返回 500' })
  assert.equal(read.tone, 'warning')
  assert.match(read.text, /宿主返回 500/)
})

test('command search matches name, alias and description', () => {
  const command = { name: 'model', aliases: ['m'], description: '切换模型', usage: 'model <id>' }
  assert.equal(commandMatches(command, 'mod'), true)
  assert.equal(commandMatches(command, '模型'), true)
  assert.equal(commandMatches(command, ''), true)
  assert.equal(commandMatches(command, 'zzz'), false)
  assert.equal(commandDetailText(command), '别名 m · model <id>')
  assert.equal(commandDetailText({ name: 'help' }), '')
})

// ---- api wire contract -------------------------------------------------------

test('run history/commands/media stay under the project namespace', async () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  const calls: string[] = []
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/alpha/' } } })
  globalThis.fetch = async (url) => {
    calls.push(String(url))
    return new Response(JSON.stringify({ run_id: 'r1', session_id: 's1', events: [], commands: [], next_cursor: '', has_more: false, source: 'aop.ListEvents' }))
  }
  const { api } = await import('../src/lib/api.ts')
  try {
    await api.runHistory('r 1', 'c/1', 50)
    await api.runCommands('r 1')
    assert.deepEqual(calls, [
      '/p/alpha/api/runs/r%201/history?after_cursor=c%2F1&limit=50',
      '/p/alpha/api/runs/r%201/commands',
    ])
    assert.equal(api.runMediaURL('r 1', 'evt-1-0.png'), '/p/alpha/api/runs/r%201/media/evt-1-0.png')
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})

// ---- render ------------------------------------------------------------------

let server: Awaited<ReturnType<typeof createTestServer>>
let api: any
let renderer: ReactTestRenderer | undefined
const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')

before(async () => {
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/alpha/', search: '', hash: '' } } })
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => {
  await server?.close()
  if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
  else Reflect.deleteProperty(globalThis, 'window')
})

function pressByLabel(label: string) {
  const node = renderer!.root.findAllByProps({ 'aria-label': label }).find((candidate) => typeof candidate.props.onClick === 'function')
  assert.ok(node, `missing button ${label}`)
  return node!
}

test('native commands load on demand and show the host menu', async () => {
  const { RunCommandsPanel } = await server.ssrLoadModule('/src/components/RunNativeSession.tsx')
  let calls = 0
  api.runCommands = async () => {
    calls++
    return {
      run_id: 'r1', session_id: 's1', source: 'aop.ListCommands',
      commands: [
        { name: 'help', description: '显示帮助' },
        { name: 'model', aliases: ['m'], usage: 'model <id>', description: '切换模型' },
      ],
    }
  }
  await act(async () => {
    renderer = create(React.createElement(RunCommandsPanel, { runID: 'r1', sessionID: 's1' }))
  })
  assert.equal(calls, 0, 'no host dial before the operator asks')
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('加载命令'))
  await act(async () => { pressByLabel('加载原生命令').props.onClick() })
  assert.equal(calls, 1)
  const text = JSON.stringify(renderer!.toJSON())
  assert.ok(text.includes('help'), text)
  assert.ok(text.includes('显示帮助'), text)
  assert.ok(text.includes('别名 m'), text)
})

test('host history loads the first page, then pages by next_cursor until the end', async () => {
  const { RunHistoryPanel } = await server.ssrLoadModule('/src/components/RunNativeSession.tsx')
  const cursors: string[] = []
  const pageOne: RunHistoryPage = {
    run_id: 'r1', session_id: 's1', source: 'aop.ListEvents', has_more: true, next_cursor: 'c2',
    events: [{ cursor: 'c1', event: { kind: 'status', state: 'eval_end', text: 'eval_end 第1/1轮：通过' } }],
  }
  const pageTwo: RunHistoryPage = {
    run_id: 'r1', session_id: 's1', source: 'aop.ListEvents', has_more: false,
    events: [{ cursor: 'c2', event: { kind: 'message', text: '收尾报告' } }],
  }
  api.runHistory = async (_id: string, after: string) => {
    cursors.push(after)
    return after ? pageTwo : pageOne
  }
  await act(async () => {
    renderer = create(React.createElement(RunHistoryPanel, { runID: 'r1', sessionID: 's1' }))
  })
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('加载第一页'))
  await act(async () => { pressByLabel('加载宿主历史第一页').props.onClick() })
  let text = JSON.stringify(renderer!.toJSON())
  assert.deepEqual(cursors, [''])
  assert.ok(text.includes('已加载'), text)
  assert.ok(text.includes('还有更多'), text)
  assert.ok(text.includes('eval_end'), text)
  await act(async () => { pressByLabel('加载更多宿主历史').props.onClick() })
  text = JSON.stringify(renderer!.toJSON())
  assert.deepEqual(cursors, ['', 'c2'])
  assert.ok(text.includes('已到末尾'), text)
  assert.ok(text.includes('收尾报告'), text)
})

test('an empty host page says so instead of rendering a blank pane', async () => {
  const { RunHistoryPanel } = await server.ssrLoadModule('/src/components/RunNativeSession.tsx')
  api.runHistory = async () => ({ run_id: 'r1', session_id: 's1', source: 'aop.ListEvents', has_more: false, events: [] })
  await act(async () => {
    renderer = create(React.createElement(RunHistoryPanel, { runID: 'r1' }))
  })
  await act(async () => { pressByLabel('加载宿主历史第一页').props.onClick() })
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('宿主返回了空页'))
})
