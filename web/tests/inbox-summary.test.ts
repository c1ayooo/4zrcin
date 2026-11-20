// The rail badge's words (lib/inbox-summary.ts).
//
// One backend read feeds the badge, the overview card and the detail list. The
// badge must never print a number it does not know: 未查询 and 不可用 are answers,
// and a badge reading 0 because the host was never asked is a lie the operator
// acts on.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { inboxBadge } from '../src/lib/inbox-summary.ts'
import type { OperatorInboxCategory, OperatorInboxSummary } from '../src/lib/types.ts'

function category(over: Partial<OperatorInboxCategory> = {}): OperatorInboxCategory {
  return { kind: 'handoff', state: 'none', count: 0, items: [], ...over }
}

function summary(over: Partial<OperatorInboxSummary> = {}): OperatorInboxSummary {
  return { categories: [], total: 0, countable: true, ...over }
}

test('a countable inbox shows its total', () => {
  const badge = inboxBadge(
    summary({
      total: 3,
      categories: [category({ state: 'ok', count: 3, items: [{ kind: 'handoff', id: 'in_a' }] })],
    }),
  )!
  assert.equal(badge.text, '3')
  assert.equal(badge.kind, 'count')
})

test('a countable inbox with nothing to do shows no badge at all, not a zero', () => {
  assert.equal(inboxBadge(summary({ total: 0, categories: [category()] })), undefined)
})

test('an unreadable category is a word, never a number', () => {
  const badge = inboxBadge(
    summary({
      total: 0,
      countable: false,
      categories: [category({ kind: 'guardrail', state: 'never_checked' })],
    }),
  )!
  assert.equal(badge.text, '未查询')
  assert.equal(badge.kind, 'unknown')
  assert.match(badge.title, /未查询不等于没有待办/)
})

test('unavailable keeps the reason and does not read as an error count', () => {
  const badge = inboxBadge(
    summary({
      total: 0,
      countable: false,
      categories: [category({ kind: 'guardrail', state: 'unavailable', reason: '没有可用会话' })],
    }),
  )!
  assert.equal(badge.text, '不可用')
  assert.match(badge.title, /没有可用会话/)
})

test('known rows next to an unknown category are shown as a lower bound', () => {
  const badge = inboxBadge(
    summary({
      total: 2,
      countable: false,
      categories: [
        category({ kind: 'handoff', state: 'ok', count: 2, items: [{ kind: 'handoff', id: 'a' }, { kind: 'handoff', id: 'b' }] }),
        category({ kind: 'guardrail', state: 'never_checked' }),
      ],
    }),
  )!
  assert.equal(badge.text, '2+')
  assert.equal(badge.kind, 'lower')
  assert.match(badge.title, /下限/)
})

test('an older backend without a summary falls back to no badge instead of guessing', () => {
  assert.equal(inboxBadge(undefined), undefined)
})
