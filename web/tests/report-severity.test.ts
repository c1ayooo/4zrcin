import { test } from 'node:test'
import assert from 'node:assert/strict'
import { tallyFindingSeverities } from '../src/lib/report-severity.ts'

/**
 * The report page shows the grades a report actually carries. It is a tally of
 * values already in `findings.json` — the console does not grade, fill a gap, or
 * fold "unlabelled" into "low". These assertions pin the vocabulary and the
 * separation, which is what keeps the display honest.
 */
test('tiers are listed in the agreed order, ungraded kept apart', () => {
  const tally = tallyFindingSeverities([
    { fact: 'f1', verdict: 'confirmed', title: 'a', severity: 'medium' },
    { fact: 'f2', verdict: 'confirmed', title: 'b', severity: 'critical' },
    { fact: 'f3', verdict: 'confirmed', title: 'c' },
    { fact: 'f4', verdict: 'confirmed', title: 'd', severity: 'HIGH' },
  ])
  assert.deepEqual(
    tally.map((t) => [t.tier, t.label, t.count]),
    [
      ['critical', '严重', 1],
      ['high', '高危', 1],
      ['medium', '中危', 1],
      ['low', '低危', 0],
      ['unlabelled', '未标注', 1],
    ],
  )
})

test('an ungraded report never invents a low finding', () => {
  const tally = tallyFindingSeverities([
    { fact: 'f1', verdict: 'confirmed', title: 'a' },
    { fact: 'f2', verdict: 'confirmed', title: 'b' },
  ])
  assert.equal(tally.find((t) => t.tier === 'low')?.count, 0)
  assert.equal(tally.find((t) => t.tier === 'unlabelled')?.count, 2)
})

test('no findings means no unlabelled bucket at all', () => {
  const tally = tallyFindingSeverities([])
  assert.equal(tally.some((t) => t.tier === 'unlabelled'), false)
  assert.equal(tally.every((t) => t.count === 0), true)
})
