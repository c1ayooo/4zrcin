// The time axis rules (graph/timeline.ts): ledger order by created_at, undated
// rows never hidden, and the one gate predicate the painter uses.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  buildTimeline,
  cursorAt,
  cursorFraction,
  revealedCount,
  revealedIn,
  timelineUsable,
  visibleAt,
} from '../src/graph/timeline.ts'
import type { GraphNode } from '../src/graph/model.ts'

function node(id: string, orderTime?: string, type: GraphNode['type'] = 'fact'): GraphNode {
  return {
    id,
    type,
    label: id,
    ...(orderTime ? { orderTime } : {}),
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    degree: 0,
    w: 6,
    h: 6,
    depth: 1,
  }
}

test('the axis is created_at order, then id, so a reload never reshuffles', () => {
  const t = buildTimeline([
    node('f:c', '2026-11-20T10:00:02Z'),
    node('f:a', '2026-11-20T10:00:00Z'),
    node('f:d', '2026-11-20T10:00:02Z'),
    node('f:b', '2026-11-20T10:00:01Z'),
  ])
  assert.deepEqual(t.order, ['f:a', 'f:b', 'f:c', 'f:d'])
  assert.equal(t.rank.get('f:d'), 3)
})

test('an undated row is counted and never hidden', () => {
  const t = buildTimeline([
    node('f:a', '2026-11-20T10:00:00Z'),
    node('f:x'),
    node('f:y', 'not-a-date'),
    node('f:b', '2026-11-20T10:00:01Z'),
  ])
  assert.deepEqual(t.order, ['f:a', 'f:b'])
  assert.deepEqual(t.untimed, ['f:x', 'f:y'])
  // Cursor 0 hides the second timed row but keeps both undated ones: a scrub may
  // only hide what it can put in order.
  assert.equal(visibleAt(t, 'f:b', 0), false)
  assert.equal(visibleAt(t, 'f:x', 0), true)
  assert.equal(visibleAt(t, 'f:y', 0), true)
})

test('the brief and boundary leaves are not on the axis at all', () => {
  const t = buildTimeline([
    node('p:project', undefined, 'project'),
    node('b:ref', undefined, 'boundary'),
    node('f:a', '2026-11-20T10:00:00Z'),
  ])
  assert.deepEqual(t.order, ['f:a'])
  assert.deepEqual(t.untimed, [])
  assert.equal(visibleAt(t, 'p:project', 0), true)
})

test('cursor and fraction round-trip, clamped at both ends', () => {
  const t = buildTimeline([
    node('f:a', '2026-11-20T10:00:00Z'),
    node('f:b', '2026-11-20T10:00:01Z'),
    node('f:c', '2026-11-20T10:00:02Z'),
    node('f:d', '2026-11-20T10:00:03Z'),
    node('f:e', '2026-11-20T10:00:04Z'),
  ])
  assert.equal(cursorAt(t, 0), 0)
  assert.equal(cursorAt(t, 1), 4)
  assert.equal(cursorAt(t, 0.5), 2)
  assert.equal(cursorAt(t, -3), 0)
  assert.equal(cursorAt(t, 9), 4)
  assert.equal(cursorFraction(t, 2), 0.5)
  assert.equal(cursorFraction(t, 99), 1)
})

test('an empty or single-row axis is not usable', () => {
  const empty = buildTimeline([])
  assert.equal(empty.order.length, 0)
  assert.equal(cursorAt(empty, 1), null)
  assert.equal(timelineUsable(empty), false)
  const one = buildTimeline([node('f:a', '2026-11-20T10:00:00Z')])
  assert.equal(timelineUsable(one), false)
  const two = buildTimeline([node('f:a', '2026-11-20T10:00:00Z'), node('f:b', '2026-11-20T10:00:01Z')])
  assert.equal(timelineUsable(two), true)
})

test('revealed count includes the cursor row and never exceeds the axis', () => {
  const t = buildTimeline([
    node('f:a', '2026-11-20T10:00:00Z'),
    node('f:b', '2026-11-20T10:00:01Z'),
    node('f:c', '2026-11-20T10:00:02Z'),
  ])
  assert.equal(revealedCount(t, 0), 1)
  assert.equal(revealedCount(t, 2), 3)
  assert.equal(revealedCount(t, 99), 3)
  assert.equal(revealedCount(buildTimeline([]), 0), 0)
})

test('the gate is rank <= cursor, and an unranked id always passes', () => {
  const rank = new Map([
    ['f:a', 0],
    ['f:b', 1],
  ])
  assert.equal(revealedIn(rank, 0, 'f:a'), true)
  assert.equal(revealedIn(rank, 0, 'f:b'), false)
  assert.equal(revealedIn(rank, 1, 'f:b'), true)
  assert.equal(revealedIn(rank, 0, 'p:project'), true)
  assert.equal(revealedIn(rank, 0, 'f:undated'), true)
})
