import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  usageBreakdownView,
  usageDetailText,
  usageNumber,
  usageSourcesText,
  usageText,
} from '../src/lib/usage-breakdown.ts'
import type { UsageBreakdown } from '../src/lib/types.ts'

// ---- pure rules: unknown is "—", a reported zero is "0" ----------------------

test('a missing number renders as — and a reported zero stays 0', () => {
  assert.equal(usageText(undefined), '—')
  assert.equal(usageText(0), '0')
  assert.equal(usageText(1234), '1,234')
  assert.equal(usageNumber(undefined, 'total'), undefined)
  // fmtInt(undefined) would print "0", so presence must be checked first.
  assert.equal(usageNumber({ total: undefined }, 'total'), undefined)
  assert.equal(usageNumber({ total: 0 }, 'total'), 0)
  assert.equal(usageDetailText(undefined), '')
  assert.equal(usageDetailText({ runs: 2, input: 1000, output: 200 }), 'runs 2 · 入 1,000 · 出 200')
  assert.equal(usageDetailText({ calls: 3, input: 10, output: 5 }), '调用 3 · 入 10 · 出 5')
})

test('the full breakdown maps commander / executor_total / combined / judge / all', () => {
  const breakdown: UsageBreakdown = {
    commander: { runs: 2, input: 1000, output: 200, total: 1200 },
    executor: { runs: 3, total: 2500 },
    support: { runs: 2, total: 900 },
    executor_total: { runs: 5, input: 3000, output: 400, total: 3400 },
    judge: {
      jev: { calls: 1, input: 100, output: 20, total: 120 },
      llm: { calls: 2, input: 60, output: 20, total: 80 },
      total: { calls: 3, input: 160, output: 40, total: 200 },
    },
    combined: { runs: 7, input: 4000, output: 600, total: 4600 },
    all: { runs: 10, input: 4160, output: 640, total: 4800 },
  }
  const view = usageBreakdownView(breakdown)
  assert.deepEqual(view.main.map((entry) => [entry.label, entry.text]), [
    ['指挥官', '1,200'],
    // executor_total wins over executor: review/report runs are part of it.
    ['执行者（含复核·报告）', '3,400'],
    ['合计（指挥官+执行者）', '4,600'],
  ])
  assert.deepEqual(view.judge.map((entry) => [entry.label, entry.text]), [['JEV', '120'], ['LLM', '80']])
  assert.equal(view.judgeTotal.text, '200')
  assert.equal(view.all.text, '4,800')
  assert.equal(view.anyData, true)
  assert.deepEqual(view.missing, [])
  assert.equal(view.note, '')
})

test('missing judge fields degrade to — with a note, and executor falls back', () => {
  const view = usageBreakdownView({ commander: { total: 1200 }, executor: { total: 800 }, combined: { total: 2000 } })
  assert.equal(view.judge[0].text, '—')
  assert.equal(view.judge[1].text, '—')
  assert.equal(view.judgeTotal.text, '—')
  assert.equal(view.all.text, '—')
  assert.equal(view.main[1].text, '800')
  assert.match(view.note, /部分分账未上报/)
  assert.match(view.note, /JEV/)
  assert.match(view.note, /全部（含裁判）/)
})

test('only-commander, all-zero and empty responses are all honest', () => {
  const onlyCommander = usageBreakdownView({ commander: { total: 42 } })
  assert.equal(onlyCommander.main[0].text, '42')
  assert.equal(onlyCommander.main[1].text, '—')
  assert.equal(onlyCommander.main[2].text, '—')
  assert.equal(onlyCommander.anyData, true)

  const zeroed: UsageBreakdown = {
    commander: { total: 0 },
    executor_total: { total: 0 },
    combined: { total: 0 },
    judge: { jev: { total: 0 }, llm: { total: 0 }, total: { total: 0 } },
    all: { total: 0 },
  }
  const zero = usageBreakdownView(zeroed)
  assert.equal(zero.anyData, true, 'a reported zero is data, not absence')
  assert.ok([...zero.main, ...zero.judge, zero.judgeTotal, zero.all].every((entry) => entry.text === '0'))
  assert.equal(zero.note, '')
  assert.deepEqual(zero.missing, [])

  const empty = usageBreakdownView(undefined)
  assert.equal(empty.anyData, false)
  assert.ok([...empty.main, ...empty.judge, empty.judgeTotal, empty.all].every((entry) => entry.text === '—'))
  assert.match(empty.note, /不估算、不补零/)

  const emptyObject = usageBreakdownView({})
  assert.equal(emptyObject.anyData, false)
  assert.match(emptyObject.note, /空分账/)
})

test('optional source availability notes render as a quiet line', () => {
  assert.equal(usageSourcesText(undefined), '')
  assert.equal(usageSourcesText({ journal: 'available', ledger: 'unavailable', nested: { x: 1 } }), 'journal=available · ledger=unavailable')
  assert.match(usageBreakdownView({ sources: { journal: 'available' } }).sources, /journal=available/)
})

// ---- api wire contract -------------------------------------------------------

test('the breakdown read stays in the project namespace', async () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  const calls: string[] = []
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/alpha/' } } })
  globalThis.fetch = async (url) => {
    calls.push(String(url))
    return new Response(JSON.stringify({ commander: { total: 0 } }))
  }
  const { api } = await import('../src/lib/api.ts')
  try {
    await api.usageBreakdown()
    assert.deepEqual(calls, ['/p/alpha/api/usage/breakdown'])
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})

// ---- render ----------------------------------------------------------------

let server: Awaited<ReturnType<typeof createTestServer>>
let TokenBreakdownCard: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

after(async () => {
  await act(async () => { renderer?.unmount() })
  await server?.close()
})

async function render(props: Record<string, unknown>) {
  if (!server) {
    server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
    TokenBreakdownCard = (await server.ssrLoadModule('/src/components/TokenBreakdownCard.tsx')).TokenBreakdownCard
  }
  await act(async () => {
    if (renderer) renderer.unmount()
    renderer = create(React.createElement(TokenBreakdownCard, props))
  })
  return JSON.stringify(renderer!.toJSON())
}

const full = (): UsageBreakdown => ({
  commander: { runs: 2, input: 1000, output: 200, total: 1200 },
  executor: { runs: 3, total: 1500 },
  support: { runs: 2, total: 900 },
  executor_total: { runs: 5, input: 3000, output: 400, total: 3400 },
  judge: {
    jev: { calls: 1, input: 100, output: 20, total: 120 },
    llm: { calls: 2, input: 60, output: 20, total: 80 },
    total: { calls: 3, input: 160, output: 40, total: 200 },
  },
  combined: { runs: 7, input: 4000, output: 600, total: 4600 },
  all: { runs: 10, input: 4160, output: 640, total: 4800 },
})

test('the card shows the six labelled figures from the mock payload', async () => {
  const text = await render({ breakdown: full() })
  for (const label of ['Token 记账', '指挥官', '执行者（含复核·报告）', '合计（指挥官+执行者）', '裁判', 'JEV', 'LLM', '全部（含裁判）']) {
    assert.ok(text.includes(label), `missing label ${label}: ${text}`)
  }
  for (const value of ['1,200', '3,400', '4,600', '120', '80', '200', '4,800']) {
    assert.ok(text.includes(value), `missing figure ${value}: ${text}`)
  }
  assert.ok(!text.includes('Token 分账暂不可用'), text)
})

test('a not-ready API renders — with an explanatory note, not a fake zero', async () => {
  const text = await render({ error: 'HTTP 404' })
  assert.ok(text.includes('—'), text)
  assert.ok(text.includes('Token 分账暂不可用'), text)
  assert.ok(text.includes('不显示估算值'), text)
  assert.ok(!text.includes('"0"'), `no fabricated zero expected: ${text}`)
})

test('an all-zero but present payload renders real zeros', async () => {
  const text = await render({
    breakdown: {
      commander: { total: 0 }, executor_total: { total: 0 }, combined: { total: 0 },
      judge: { jev: { total: 0 }, llm: { total: 0 }, total: { total: 0 } }, all: { total: 0 },
    },
  })
  assert.ok(text.includes('"0"'), text)
  assert.ok(!text.includes('"—"'), text)
})
