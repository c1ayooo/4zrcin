// The graph's node panel ties back to the run ledger: one line names the latest
// executor run, its state in the run page's words, its duration and start, and
// (when the shell provides the route) opens it. It exists for every state, not
// only for a cancellation.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import type { Run } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let RunSummaryLine: React.ComponentType<{ run: Run; onOpenRun?: (runID: string) => void }>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  RunSummaryLine = (await server.ssrLoadModule('/src/components/graph/RunSummaryLine.tsx')).RunSummaryLine
})

after(async () => {
  await server?.close()
})

function run(over: Partial<Run> = {}): Run {
  return {
    id: 'r_d05stop',
    intent_id: 'in_d05',
    node: 'local',
    status: 'canceled',
    cancel_cause: 'stop_loss',
    exit_reason: '止损：d-05 flag 已 submit，回收槽位起新题。',
    started_at: '2026-11-24T22:00:56+08:00',
    ended_at: '2026-11-24T22:02:15+08:00',
    ...over,
  } as Run
}

test('the latest run is one line in the run page vocabulary', () => {
  const html = renderToStaticMarkup(
    React.createElement(RunSummaryLine, { run: run(), onOpenRun: () => {} }),
  )
  assert.match(html, /最近运行/)
  assert.match(html, /r_d05stop/)
  assert.match(html, /止损取消/, 'the structured cause is the same label the run page shows')
  assert.match(html, /1m19s/, 'how long the attempt took')
  assert.match(html, /运行详情/, 'the route back to the ledger')
})

test('without a route callback there is no link', () => {
  const html = renderToStaticMarkup(React.createElement(RunSummaryLine, { run: run() }))
  assert.doesNotMatch(html, /运行详情/)
})

test('done and failed attempts use the same vocabulary', () => {
  const done = renderToStaticMarkup(
    React.createElement(RunSummaryLine, { run: run({ status: 'done', cancel_cause: undefined, exit_reason: 'completed' }) }),
  )
  assert.match(done, /完成/)
  const failed = renderToStaticMarkup(
    React.createElement(RunSummaryLine, { run: run({ status: 'failed', cancel_cause: undefined, exit_reason: '评估未通过' }) }),
  )
  assert.match(failed, /失败/)
})
