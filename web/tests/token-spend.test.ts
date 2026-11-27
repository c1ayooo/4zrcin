// Overview token-spend panel (docs plan: composition + 24h rhythm + coverage).
//
// The rules under test are the project's accounting honesty rules applied to a
// compact view: a number is model-reported or it is absent ("—", never 0), and
// shares are only printed when every spender reported — known parts are never
// renormalised to a fake 100%.
import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  usageBreakdownView,
  usageComposition,
  usageCoverage,
  usageHourlySeries,
} from '../src/lib/usage-breakdown.ts'
import type { UsageBreakdown } from '../src/lib/types.ts'

// ---- composition ------------------------------------------------------------

test('shares are printed only when every spender reported', () => {
  const view = usageBreakdownView({
    commander: { total: 1000 },
    executor_total: { total: 3000 },
    judge: { total: { total: 1000 } },
    all: { total: 5000 },
  })
  const composition = usageComposition(view)
  assert.deepEqual(
    composition.segments.map((segment) => [segment.key, segment.text, segment.pctText]),
    [
      ['commander', '1,000', '20%'],
      ['executor', '3,000', '60%'],
      ['judge', '1,000', '20%'],
    ],
  )
  assert.equal(composition.totalText, '5,000')
  assert.equal(composition.shareKnown, true)
  assert.deepEqual(composition.unknown, [])
})

test('a missing spender keeps — and disables shares instead of renormalising', () => {
  // No judge bucket at all: 1,000 + 3,000 must not become 25% / 75%.
  const view = usageBreakdownView({
    commander: { total: 1000 },
    executor_total: { total: 3000 },
    all: { total: 4000 },
  })
  const composition = usageComposition(view)
  assert.equal(composition.segments[0].text, '1,000')
  assert.equal(composition.segments[2].text, '—')
  assert.equal(composition.segments[0].pctText, '—', 'known parts must not be renormalised')
  assert.equal(composition.segments[1].pctText, '—')
  assert.equal(composition.segments[2].pctText, '—')
  assert.equal(composition.shareKnown, false)
  assert.deepEqual(composition.unknown, ['裁判'])
})

test('a reported zero is a real 0%, not an unknown', () => {
  const view = usageBreakdownView({
    commander: { total: 0 },
    executor_total: { total: 0 },
    judge: { total: { total: 0 } },
    all: { total: 0 },
  })
  const composition = usageComposition(view)
  assert.deepEqual(
    composition.segments.map((segment) => [segment.text, segment.pctText]),
    [
      ['0', '0%'],
      ['0', '0%'],
      ['0', '0%'],
    ],
  )
  assert.equal(composition.shareKnown, true)
})

test('a sub-1% share reads <1%, never 0%', () => {
  const view = usageBreakdownView({
    commander: { total: 1 },
    executor_total: { total: 999 },
    judge: { total: { total: 0 } },
    all: { total: 1000 },
  })
  const composition = usageComposition(view)
  assert.equal(composition.segments[0].pctText, '<1%')
  assert.equal(composition.segments[2].pctText, '0%')
})

// ---- 24h rhythm -------------------------------------------------------------

// Local-clock fixtures: bucketing is by local hour, so building the instants
// with `new Date(y, m, d, h, m)` keeps the test independent of the host TZ.
const at = (day: number, hour: number, minute = 0) =>
  new Date(2026, 10, day, hour, minute).toISOString()
const NOW = new Date(2026, 10, 27, 15, 30).getTime()

test('the 24h series buckets by start hour and keeps the current hour partial', () => {
  const runs = [
    { started_at: at(27, 15, 10), tokens: 500 }, // current, in progress
    { started_at: at(27, 14, 5), tokens: 100 },
    { started_at: at(27, 12), tokens: 7 },
    { started_at: at(26, 10), tokens: 900 }, // previous 24h window
  ]
  const series = usageHourlySeries(runs, { now: NOW })
  assert.equal(series.buckets.length, 24)
  const last = series.buckets[23]
  assert.equal(last.label, '15:00')
  assert.equal(last.partial, true)
  assert.equal(last.tokens, 500)
  assert.equal(series.buckets[22].label, '14:00')
  assert.equal(series.buckets[22].tokens, 100)
  assert.equal(series.buckets[22].partial, false)
  assert.equal(series.total, 607)
  assert.equal(series.totalText, '607')
  assert.equal(series.previous, 900)
  // 607 vs 900 is -32.6% → -33. A literal, not a recomputation.
  assert.equal(series.deltaPct, -33)
  // The in-progress hour is not eligible to be the peak even when it is largest.
  assert.deepEqual(series.peak, { label: '14:00', tokens: 100 })
  assert.equal(series.unknownRuns, 0)
  assert.equal(series.excludesJudge, true)
})

test('the window boundary is the first bucket start, not "24 hours ago from now"', () => {
  const series = usageHourlySeries(
    [
      { started_at: at(26, 16), tokens: 6 }, // first bucket
      { started_at: at(26, 15, 59), tokens: 4 }, // previous window
      { started_at: at(25, 15, 59), tokens: 99 }, // outside both windows
    ],
    { now: NOW },
  )
  assert.equal(series.buckets[0].label, '16:00')
  assert.equal(series.buckets[0].tokens, 6)
  assert.equal(series.previous, 4)
  assert.equal(series.total, 6)
})

test('runs without reported tokens stay unknown: no zero, no invented sum', () => {
  const series = usageHourlySeries(
    [
      { started_at: at(27, 14, 5) }, // unknown usage
      { started_at: at(27, 13, 5), tokens: 20 },
    ],
    { now: NOW },
  )
  const hour14 = series.buckets.find((bucket) => bucket.label === '14:00')!
  assert.equal(hour14.runs, 1)
  assert.equal(hour14.unknownRuns, 1)
  assert.equal(hour14.tokens, undefined, 'an unreported hour is unknown, never a measured 0')
  assert.equal(series.unknownRuns, 1)
  assert.equal(series.total, 20)
})

test('a window with runs but no reported usage has an unknown total, not 0', () => {
  const series = usageHourlySeries([{ started_at: at(27, 14, 5) }], { now: NOW })
  assert.equal(series.total, undefined)
  assert.equal(series.totalText, '—')
  assert.equal(series.peak, undefined)
})

test('an empty window is a measured zero; a delta needs a reported previous window', () => {
  const empty = usageHourlySeries([], { now: NOW })
  assert.equal(empty.total, 0)
  assert.equal(empty.totalText, '0')
  assert.equal(empty.previous, 0)
  assert.equal(empty.deltaPct, undefined, 'no division by a zero previous window')

  const unreported = usageHourlySeries(
    [{ started_at: at(26, 10) }, { started_at: at(27, 14, 5), tokens: 30 }],
    { now: NOW },
  )
  assert.equal(unreported.previous, undefined)
  assert.equal(unreported.deltaPct, undefined, 'a partial previous window must not produce a percentage')
})

// ---- coverage ---------------------------------------------------------------

test('coverage counts unreported usage and surfaces the backend reasons', () => {
  const coverage = usageCoverage({
    all: { total: 100, unknown_usage_runs: 2, missing_usage: 3 },
    unavailable: ['2 个 run 没有上报 usage', '3 条 judge_usage 缺 total 字段'],
  })
  assert.equal(coverage.unknownRuns, 2)
  assert.equal(coverage.missingCalls, 3)
  assert.deepEqual(coverage.unavailable, [
    '2 个 run 没有上报 usage',
    '3 条 judge_usage 缺 total 字段',
  ])
  assert.match(coverage.text, /2 个 run 未上报/)
  assert.match(coverage.text, /3 次调用缺用量/)
})

test('fully reported data has no caveat line', () => {
  const coverage = usageCoverage({ all: { total: 100 } })
  assert.equal(coverage.unknownRuns, 0)
  assert.equal(coverage.missingCalls, 0)
  assert.deepEqual(coverage.unavailable, [])
  assert.equal(coverage.text, '')
})

// ---- render: overview panel -------------------------------------------------

const fullBreakdown = (): UsageBreakdown => ({
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

const recentRuns = [
  { started_at: at(27, 15, 10), tokens: 500 }, // in-progress hour
  { started_at: at(27, 14, 5), tokens: 100 },
  { started_at: at(27, 12), tokens: 7 },
  { started_at: at(26, 10), tokens: 900 }, // previous 24h
]

let server: Awaited<ReturnType<typeof createTestServer>>
let TokenSpendPanel: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

after(async () => {
  await act(async () => { renderer?.unmount() })
  await server?.close()
})

async function renderPanel(props: Record<string, unknown>) {
  if (!server) {
    server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
    TokenSpendPanel = (await server.ssrLoadModule('/src/components/TokenSpendPanel.tsx')).TokenSpendPanel
  }
  await act(async () => {
    if (renderer) renderer.unmount()
    renderer = create(React.createElement(TokenSpendPanel, { now: NOW, ...props }))
  })
  return renderer!
}

test('the panel renders the three spenders, their shares, and the 24h rhythm', async () => {
  const panel = await renderPanel({ breakdown: fullBreakdown(), runs: recentRuns })
  const text = JSON.stringify(panel.toJSON())
  for (const part of [
    'Token 消耗',
    '只读 · 模型上报',
    '消耗结构 · 项目累计',
    '指挥官',
    '执行者（含复核·报告）',
    '裁判',
    '25%',
    '71%',
    '近 24 小时',
    '前 24h',
    '607',
    '-33%',
  ]) {
    assert.ok(text.includes(part), `panel is missing "${part}"`)
  }
  panel.root.findByProps({ 'data-testid': 'overview-token-spend' })
  const bar = panel.root.findByProps({ 'data-testid': 'token-composition-bar' })
  assert.match(bar.props['aria-label'], /指挥官 1,200（25%）/)
  assert.match(bar.props['aria-label'], /执行者（含复核·报告） 3,400（71%）/)
  const hourly = panel.root.findByProps({ 'data-testid': 'token-hourly-bars' })
  assert.match(hourly.props['aria-label'], /607/)
})

test('a missing spender keeps — and never prints renormalised shares', async () => {
  const panel = await renderPanel({
    breakdown: {
      commander: { total: 1000 },
      executor_total: { total: 3000 },
      all: { total: 4000 },
    },
    runs: [],
  })
  const text = JSON.stringify(panel.toJSON())
  assert.ok(text.includes('—'), 'the missing judge is unknown, not 0')
  assert.ok(!text.includes('25%'), '1,000 of a known-only sum must not become 25%')
  assert.ok(!text.includes('75%'))
  assert.ok(text.includes('占比不显示'), text)
})

test('loading without data shows a skeleton, never a fabricated number', async () => {
  const panel = await renderPanel({ loading: true })
  panel.root.findByProps({ 'data-testid': 'token-spend-loading' })
  const text = JSON.stringify(panel.toJSON())
  assert.ok(!text.includes('"0"'), `no fabricated zero expected: ${text}`)
  assert.ok(!text.includes('所有 run 都上报了用量'), 'coverage is unknown while loading')
})

test('no data and no error says so instead of showing zeros', async () => {
  const text = JSON.stringify((await renderPanel({})).toJSON())
  assert.ok(text.includes('还没有读到 Token 分账'), text)
  assert.ok(text.includes('不估算、不补零'), text)
})

test('an API error keeps the last numbers and marks the split unavailable', async () => {
  const text = JSON.stringify((await renderPanel({ breakdown: fullBreakdown(), runs: recentRuns, error: 'HTTP 404' })).toJSON())
  assert.ok(text.includes('4,800'), 'the last successful total stays on screen')
  assert.ok(text.includes('Token 分账暂不可用'), text)
})

test('coverage names unreported runs and repeats the backend reasons', async () => {
  const breakdown: UsageBreakdown = {
    ...fullBreakdown(),
    all: { ...fullBreakdown().all!, unknown_usage_runs: 2, missing_usage: 1 },
    unavailable: ['2 个 run 没有上报 usage'],
  }
  const text = JSON.stringify((await renderPanel({ breakdown, runs: recentRuns })).toJSON())
  assert.ok(text.includes('2 个 run 未上报用量'), text)
  assert.ok(text.includes('1 次调用缺用量'), text)
  assert.ok(text.includes('2 个 run 没有上报 usage'), text)
})
