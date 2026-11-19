import { test } from 'node:test'
import assert from 'node:assert/strict'
import { freshnessOf, STALE_PERIODS } from '../src/lib/freshness.ts'

const INTERVAL = 5000
const NOW = Date.parse('2026-11-19T16:00:00+08:00')

test('a poll that has not answered yet says so instead of claiming live', () => {
  assert.deepEqual(
    { tone: freshnessOf({ loading: true }, INTERVAL, NOW).tone, label: freshnessOf({ loading: true }, INTERVAL, NOW).label },
    { tone: 'idle', label: '连接中…' },
  )
  const settled = freshnessOf({ loading: false }, INTERVAL, NOW)
  assert.equal(settled.tone, 'idle')
  assert.equal(settled.label, '尚未取到数据')
})

test('a successful read inside two periods is live and names the time', () => {
  const state = freshnessOf({ updatedAt: NOW - 3000, loading: false }, INTERVAL, NOW)
  assert.equal(state.tone, 'live')
  assert.match(state.label, /^实时 · \d{2}:\d{2}:\d{2} 更新$/)
})

test('the boundary is inclusive: exactly two missed periods is still live', () => {
  const atBoundary = freshnessOf({ updatedAt: NOW - INTERVAL * STALE_PERIODS, loading: false }, INTERVAL, NOW)
  assert.equal(atBoundary.tone, 'live')
  const past = freshnessOf({ updatedAt: NOW - INTERVAL * STALE_PERIODS - 1, loading: false }, INTERVAL, NOW)
  assert.equal(past.tone, 'stale')
  assert.match(past.label, /未更新/)
})

test('stale data says how old it is rather than going quiet', () => {
  const stale = freshnessOf({ updatedAt: NOW - 65_000, loading: false }, INTERVAL, NOW)
  assert.equal(stale.tone, 'stale')
  assert.match(stale.label, /数据已 1 分钟 未更新/)
  assert.match(stale.detail, /最后一次成功在/)
})

test('a failed read keeps the last success time and never reads as calm', () => {
  const withSuccess = freshnessOf({ updatedAt: NOW - 30_000, error: '连接被拒绝', loading: false }, INTERVAL, NOW)
  assert.equal(withSuccess.tone, 'lost')
  assert.match(withSuccess.label, /数据流中断/)
  assert.match(withSuccess.label, /上次成功 \d{2}:\d{2}:\d{2}/)
  assert.match(withSuccess.detail, /连接被拒绝/)

  const never = freshnessOf({ error: '连接被拒绝', loading: false }, INTERVAL, NOW)
  assert.equal(never.tone, 'lost')
  assert.match(never.label, /从未成功/)
  assert.match(never.detail, /连接被拒绝/)
})
