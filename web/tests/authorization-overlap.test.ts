// Overlapping request states (lib/authorization-overlap.ts).
//
// Two real rows from the sampled ledger: a request dismissed while its
// resolution says the work resumed, and a still-pending request whose body
// opens with 【报告已交付】. The console shows both fields; it does not decide
// which one is true, and it must not silently show only one.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { authorizationOverlap, authorizationOverlapNote } from '../src/lib/authorization-overlap.ts'

const dismissedButResumed = {
  status: 'dismissed',
  resolution: 'work_resumed',
  resolved_at: '2026-11-20T20:10:00+08:00',
  action: '扩大测试到 10.0.0.0/24',
}

const pendingButBodySaysDelivered = {
  status: 'pending',
  action: '【报告已交付（20:50）】报告已挂到项目报告页，等待复核',
}

test('已驳回 + 工作已恢复 shows both sides instead of one', () => {
  const overlap = authorizationOverlap(dismissedButResumed)
  assert.equal(overlap.statusLabel, '已驳回')
  assert.equal(overlap.resolution, 'work_resumed')
  assert.equal(overlap.contradicts, true)
  const note = authorizationOverlapNote(overlap)
  assert.match(note, /已驳回/)
  assert.match(note, /work_resumed/)
})

test('待处理 + 正文说已交付 is called out, not silently resolved', () => {
  const overlap = authorizationOverlap(pendingButBodySaysDelivered)
  assert.equal(overlap.statusLabel, '等你决定')
  assert.equal(overlap.bodyState, '【报告已交付（20:50）】')
  assert.equal(overlap.contradicts, true)
  assert.match(authorizationOverlapNote(overlap), /以状态为准/)
})

test('a plain pending request has nothing to explain', () => {
  const overlap = authorizationOverlap({ status: 'pending', action: '需要你确认证据目录的保留期' })
  assert.equal(overlap.contradicts, false)
  assert.equal(overlap.bodyState, undefined)
  assert.equal(authorizationOverlapNote(overlap), '')
})

test('a resolved request keeps its resolution as the second side', () => {
  const overlap = authorizationOverlap({ status: 'resolved', resolution: '已授权，按最小验证做', resolved_at: '2026-11-20T20:00:00+08:00', action: 'x' })
  assert.equal(overlap.statusLabel, '已处理')
  assert.equal(overlap.resolution, '已授权，按最小验证做')
  assert.equal(overlap.contradicts, false)
})

test('an unrecorded status is said out loud, not mapped to pending', () => {
  const overlap = authorizationOverlap({ status: '', action: '' })
  assert.equal(overlap.statusLabel, '状态未记录')
})

test('a bracket that is not a status marker is not treated as one', () => {
  assert.equal(authorizationOverlap({ status: 'pending', action: '【' }).bodyState, undefined)
  assert.equal(authorizationOverlap({ status: 'pending', action: '目标未达成，没有在办意图' }).bodyState, undefined)
})
