// Record-level inbox links (lib/record-links.ts).
//
// A todo row names one record; the detail page reads the same id from the URL.
// A stale or foreign id must resolve to "not found" — never to the first row,
// which would show the operator a different record than the one they clicked.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { findAuthorization, findGoal } from '../src/lib/record-links.ts'
import type { AuthorizationRequest, Goal, OperatorInbox } from '../src/lib/types.ts'

const request: AuthorizationRequest = {
  id: 'req_a',
  action: '扩大测试到 10.0.0.0/24',
  asset: '10.0.0.0/24',
  status: 'pending',
  created_at: '2026-11-20T09:30:00+08:00',
  updated_at: '2026-11-20T09:30:00+08:00',
}

const inbox: OperatorInbox = {
  authorization: [request],
  handoff: [],
  acceptance: [],
  warnings: [],
  total: 1,
}

const goal: Goal = {
  id: 'g_a',
  statement: '核对边界',
  met: true,
  created_at: '2026-11-20T09:30:00+08:00',
  receipt_stale: true,
  receipt_changed: ['f_2'],
}

test('an authorization id resolves to that request', () => {
  assert.equal(findAuthorization(inbox, 'req_a'), request)
})

test('an unknown or missing authorization id resolves to nothing, not the first row', () => {
  assert.equal(findAuthorization(inbox, 'req_missing'), undefined)
  assert.equal(findAuthorization(inbox, ''), undefined)
  assert.equal(findAuthorization(inbox, null), undefined)
  assert.equal(findAuthorization(undefined, 'req_a'), undefined)
})

test('a goal id resolves to that goal, and a stale id does not fall back', () => {
  assert.equal(findGoal([goal], 'g_a'), goal)
  assert.equal(findGoal([goal], 'g_missing'), undefined)
  assert.equal(findGoal([goal], ''), undefined)
  assert.equal(findGoal(undefined, 'g_a'), undefined)
})
