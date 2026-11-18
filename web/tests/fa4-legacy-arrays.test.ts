// FA4/FA5: the ordering helpers used to copy before sorting because the shared
// tsconfig stopped at ES2020. `web/tsconfig.json` now raises lib to ES2023, so
// the helpers use `toSorted()` (FA5) while these tests pin the stronger
// invariant that still matters: no caller array may be reordered in place.
// Keeping the test even after the migration is deliberate — it catches an
// accidental in-place `.sort()` reappearing.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { sortReviewsNewestFirst } from '../src/lib/claim.ts'
import { groupRunHistory } from '../src/lib/run-history.ts'
import { orderReviewEntries, type ReviewEntry } from '../src/lib/run-review.ts'
import type { Review, Run } from '../src/lib/types.ts'

test('claim review ordering copies instead of reordering the caller list', () => {
  const reviews = [
    { id: 'old', fact_id: 'f_1', verdict: 'pass', created_at: '2026-11-10T00:00:00Z' },
    { id: 'new', fact_id: 'f_1', verdict: 'pass', created_at: '2026-11-12T00:00:00Z' },
  ] as Review[]
  const sorted = sortReviewsNewestFirst(reviews)
  assert.deepEqual(sorted.map((review) => review.id), ['new', 'old'])
  assert.deepEqual(reviews.map((review) => review.id), ['old', 'new'])
  assert.notEqual(sorted, reviews)
})

test('run history grouping keeps the caller run order', () => {
  const run = (id: string, seconds: number): Run => ({
    id,
    intent_id: 'in_a',
    node: 'local',
    status: 'done',
    events: 0,
    declared_facts: 0,
    started_at: new Date(Date.UTC(2026, 10, 10, 0, 0, seconds)).toISOString(),
  })
  const runs = [run('old', 0), run('new', 20)]
  const groups = groupRunHistory(runs)
  assert.deepEqual(groups[0].runs.map((row) => row.id), ['new', 'old'])
  assert.deepEqual(runs.map((row) => row.id), ['old', 'new'])
})

test('token ordering copies the review entries', () => {
  const entry = (key: string, order: number, totalTokens: number): ReviewEntry => ({
    key,
    order,
    kind: 'model',
    label: '模型调用',
    state: 'completed',
    eventIndex: order,
    at: '2026-11-10T00:00:00Z',
    totalTokens,
  })
  const entries = [entry('cheap', 1, 10), entry('expensive', 2, 500)]
  const ordered = orderReviewEntries(entries, 'tokens')
  assert.deepEqual(ordered.map((row) => row.key), ['expensive', 'cheap'])
  assert.deepEqual(entries.map((row) => row.key), ['cheap', 'expensive'])
  // The time view is a copy as well, never the caller's array.
  const byTime = orderReviewEntries(entries, 'time')
  assert.notEqual(byTime, entries)
  assert.deepEqual(byTime.map((row) => row.key), ['cheap', 'expensive'])
})
