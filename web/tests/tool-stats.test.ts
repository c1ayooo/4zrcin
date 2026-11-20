// Tool leaderboard arithmetic (lib/tool-stats.ts). The point of these tests is the
// limit as much as the maths: an unknown rate is not a zero, and an unparsable
// timestamp is not "most recent".
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  TOOL_ROWS_VISIBLE,
  averageMs,
  failureRate,
  formatMs,
  formatRate,
  sortToolRows,
  type ToolStatRow,
} from '../src/lib/tool-stats.ts'

const row = (tool: string, extra: Partial<ToolStatRow> = {}): ToolStatRow => ({
  tool,
  calls: 1,
  runs: 1,
  completed: 1,
  failures: 0,
  duration_ms: 100,
  ...extra,
})

test('failure rate is undefined until something completed', () => {
  assert.equal(failureRate(row('a', { completed: 0, failures: 0 })), null)
  assert.equal(failureRate(row('a', { completed: undefined, failures: 2 })), null)
  assert.equal(failureRate(row('a', { completed: 4, failures: 1 })), 0.25)
})

test('average duration is undefined without completed results or duration', () => {
  assert.equal(averageMs(row('a', { completed: 0, duration_ms: 500 })), null)
  assert.equal(averageMs(row('a', { completed: 2, duration_ms: 0 })), null)
  assert.equal(averageMs(row('a', { completed: 2, duration_ms: 500 })), 250)
})

test('rates below ten percent keep one decimal', () => {
  assert.equal(formatRate(0.034), '3.4%')
  assert.equal(formatRate(0.25), '25%')
  assert.equal(formatRate(null), '—')
})

test('durations stay readable across three magnitudes', () => {
  assert.equal(formatMs(0.4), '<1ms')
  assert.equal(formatMs(12.4), '12ms')
  assert.equal(formatMs(1240), '1.2s')
  assert.equal(formatMs(12_400), '12s')
  assert.equal(formatMs(125_000), '2m05s')
  assert.equal(formatMs(null), '—')
})

test('the default order is calls, then name', () => {
  const rows = [row('b', { calls: 2 }), row('a', { calls: 2 }), row('c', { calls: 9 })]
  assert.deepEqual(sortToolRows(rows, 'calls').map((r) => r.tool), ['c', 'a', 'b'])
})

test('a failing tool reaches the top of the failure order, unknown last', () => {
  const rows = [
    row('quiet', { calls: 100, completed: 100, failures: 0 }),
    row('flaky', { calls: 4, completed: 4, failures: 3 }),
    row('unmeasured', { calls: 50, completed: 0, failures: 0 }),
  ]
  assert.deepEqual(sortToolRows(rows, 'failures').map((r) => r.tool), ['flaky', 'quiet', 'unmeasured'])
})

test('slowest average wins, and an unknown duration stays last', () => {
  const rows = [
    row('fast', { calls: 3, completed: 3, duration_ms: 30 }),
    row('slow', { calls: 1, completed: 1, duration_ms: 900 }),
    row('nolog', { calls: 8, completed: 0, duration_ms: 0 }),
  ]
  assert.deepEqual(sortToolRows(rows, 'duration').map((r) => r.tool), ['slow', 'fast', 'nolog'])
})

test('recent order puts the latest call first and undated rows last', () => {
  const rows = [
    row('old', { last_at: '2026-11-20T10:00:00Z' }),
    row('new', { last_at: '2026-11-20T12:00:00Z' }),
    row('never', { last_at: undefined }),
    row('broken', { last_at: 'not-a-date' }),
  ]
  assert.deepEqual(sortToolRows(rows, 'recent').map((r) => r.tool), ['new', 'old', 'broken', 'never'])
})

test('the visible list is capped at ten, and the count is separate', () => {
  assert.equal(TOOL_ROWS_VISIBLE, 10)
})
