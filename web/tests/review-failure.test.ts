// The 状态 page and the graph panel both show a `review_failed` direction. The
// label has to say which of two things happened, because the operator's next
// move differs: retry (nothing decided it) versus change the conclusion (a
// verifier said no). These cases pin the derivation and keep every surface on
// the same wording.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  classifyReviewFailure,
  reviewFailureHint,
  reviewFailureLabel,
} from '../src/lib/review-failure.ts'

test('a verdict that said no is not the same as a review that never ran', () => {
  const rejected = classifyReviewFailure([{ id: 'f_a', verdicts: ['pass', 'needs_evidence'] }])
  assert.equal(rejected.kind, 'review_rejected')
  assert.deepEqual(rejected.rejected, ['f_a'])

  const finishedClean = classifyReviewFailure([{ id: 'f_b', verdicts: [] }])
  assert.equal(finishedClean.kind, 'review_unfinished', 'no review row at all means nothing decided it')
  assert.deepEqual(finishedClean.rejected, [])

  // A phase that died before it could name a fact is the same unfinished case.
  assert.equal(classifyReviewFailure([]).kind, 'review_unfinished')
})

test('one rejecting verdict among several facts is enough to call it rejected', () => {
  const mixed = classifyReviewFailure([
    { id: 'f_ok', verdicts: ['pass'] },
    { id: 'f_no', verdicts: ['reject'] },
  ])
  assert.equal(mixed.kind, 'review_rejected')
  assert.deepEqual(mixed.rejected, ['f_no'], 'only the rejected fact is named')
})

test('a failed read is reported as unread, never as "nothing ran"', () => {
  const unread = classifyReviewFailure([
    { id: 'f_c', verdicts: null },
    { id: 'f_d', verdicts: [] },
  ])
  assert.equal(unread.kind, 'review_unfinished')
  assert.deepEqual(unread.unread, ['f_c'])
  assert.match(reviewFailureLabel(unread), /没读到/)
  assert.doesNotMatch(reviewFailureLabel(unread), /可重试/, 'must not promise a retry it cannot see')
})

test('labels and hints name the next action, not just the state', () => {
  const rejected = classifyReviewFailure([{ id: 'f_a', verdicts: ['reject'] }])
  assert.equal(reviewFailureLabel(rejected), '复核判定不通过')
  assert.match(reviewFailureHint(rejected), /重试不会自动通过/)

  const unfinished = classifyReviewFailure([{ id: 'f_b', verdicts: [] }])
  assert.equal(reviewFailureLabel(unfinished), '复核没跑完（可重试）')
  assert.match(reviewFailureHint(unfinished), /不代表结论被否/)

  // Before the read lands there is nothing honest to claim beyond the status.
  assert.equal(reviewFailureLabel(undefined), '复核未通过')
  assert.doesNotMatch(reviewFailureHint(undefined), /判定不通过：/)
})

test('every surface that shows a review failure uses the derived label', () => {
  const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')
  for (const file of ['views/ProjectState.tsx', 'components/graph/GraphNodePanel.tsx']) {
    const source = read(file)
    assert.match(source, /reviewFailureLabel\(/, `${file} uses the derived label`)
    assert.match(source, /review_failed_facts/, `${file} names the facts still under review`)
  }
  // The roster row must not fall back to one word for both causes.
  assert.doesNotMatch(read('views/ProjectState.tsx'), /复核未通过，可原地重试\/续跑/)
  assert.match(read('components/graph/GraphLegend.tsx'), /复核未通过（判定 \/ 没跑完）/)
})
