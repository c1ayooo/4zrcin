// The overview's first block (lib/project-status.ts).
//
// The block reports what the board already says and never rules on 「已收口」:
// that verdict depends on pending directions, unread messages and a running
// closeout, so the backend owns it. These cases pin the reading order of
// "what happens next" and the fact that a pause reason is relayed, not guessed.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { projectStatusSummary } from '../src/lib/project-status.ts'
import type { BoardResponse } from '../src/lib/types.ts'

function board(over: Partial<BoardResponse> = {}): BoardResponse {
  return {
    generated_at: '2026-11-20T09:30:00+08:00',
    case: 'fixture',
    project_paused: false,
    intents: { total: 4, running: 1, waiting: 0, done: 3, pending: 0, held: 0, failed: 0, parked: 0 },
    facts: { total: 6, verified: 4, by_kind: {}, by_origin: {}, with_evidence: 5, reviewable_unverified: 1, notes: 1 },
    evidence: 4,
    runs: { total: 4, running: 1, done: 3, failed: 0, canceled: 0 },
    goals: { total: 2, met: 1, open: [] },
    decider: { enabled: true, paused: false, rounds: 3, tokens_total: 3200 },
    playbooks: { confirmed: 0, candidates: 0, retired: 0, archived: 0 },
    redispatch: { total: 0 },
    open_intents: [],
    running_intents: [],
    waiting_intents: [],
    recent_facts: [],
    frontier: [],
    handoff_warnings: [],
    ...over,
  } as BoardResponse
}

test('a paused project relays the backend reason instead of inventing one', () => {
  const s = projectStatusSummary(
    board({ project_paused: true, decider: { ...board().decider, paused: true, paused_reason: '操作员暂停' } }),
  )!
  assert.equal(s.paused, true)
  assert.equal(s.reason, '操作员暂停')
  assert.match(s.nextStep, /操作员暂停/)
  assert.match(s.nextStep, /继续项目/)
})

test('a pause without a reason still says how to leave it', () => {
  const s = projectStatusSummary(board({ project_paused: true }))!
  assert.equal(s.reason, '')
  assert.match(s.nextStep, /^暂停中/)
})

test('a failed run outranks queued directions in the next-step line', () => {
  const s = projectStatusSummary(
    board({
      runs: { total: 4, running: 0, done: 3, failed: 2, canceled: 0 },
      intents: { total: 4, running: 0, waiting: 0, done: 3, pending: 2, held: 0, failed: 0, parked: 0 },
    }),
  )!
  assert.equal(s.failedRuns, 2)
  assert.match(s.nextStep, /2 个失败运行/)
})

test('blocked directions are counted from the frontier, not from the intent total', () => {
  const s = projectStatusSummary(
    board({
      intents: { total: 4, running: 0, waiting: 0, done: 3, pending: 2, held: 0, failed: 0, parked: 0 },
      frontier: [
        { id: 's_1', title: '可派', ready: true },
        { id: 's_2', title: '被挡', ready: false, blocked_on: ['f_2'] },
      ],
    }),
  )!
  assert.equal(s.directionsBlocked, 1)
  assert.equal(s.directionsPending, 2)
  assert.match(s.nextStep, /1 个方向被前提挡住/)
})

test('held directions are not counted as waiting to be dispatched', () => {
  const s = projectStatusSummary(
    board({ intents: { total: 2, running: 0, waiting: 0, done: 0, pending: 3, held: 2, failed: 0, parked: 0 } }),
  )!
  assert.equal(s.directionsPending, 1)
})

test('a quiet engagement says so instead of claiming progress', () => {
  const s = projectStatusSummary(
    board({
      intents: { total: 1, running: 0, waiting: 0, done: 1, pending: 0, held: 0, failed: 0, parked: 0 },
      runs: { total: 1, running: 0, done: 1, failed: 0, canceled: 0 },
    }),
  )!
  assert.match(s.nextStep, /没有排队、没有阻塞/)
})

test('no board means no summary, so the page keeps its skeleton', () => {
  assert.equal(projectStatusSummary(undefined), undefined)
  assert.equal(projectStatusSummary(null), undefined)
})
