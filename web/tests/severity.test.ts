import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  SEVERITY_TIERS,
  severityBucket,
  severityDot,
  severityLabel,
  severityVariant,
} from '../src/lib/format.ts'

/**
 * One severity word has to look the same on every page: the template library,
 * the vulnerability rows and the graph inspector all read this one table. The
 * console only paints and labels — ordering and ranking come from the server —
 * so what is asserted here is vocabulary, not precedence.
 */
test('the four tiers keep their operator-facing words', () => {
  assert.deepEqual(
    SEVERITY_TIERS.map((s) => severityLabel(s)),
    ['严重', '高危', '中危', '低危'],
  )
})

test('an unknown or absent level is shown as given, never guessed into a tier', () => {
  assert.equal(severityLabel('deferred'), 'deferred')
  assert.equal(severityLabel(''), '')
  assert.equal(severityLabel(undefined), '')
  assert.equal(severityVariant('deferred'), 'muted')
  assert.equal(severityDot('deferred'), 'bg-ink-3')
})

test('the same word gets the same badge and dot everywhere', () => {
  assert.equal(severityVariant('high'), 'caution')
  assert.equal(severityVariant('HIGH'), 'caution')
  assert.equal(severityDot('high'), 'bg-caution')
  assert.equal(severityVariant('low'), 'info')
  assert.equal(severityDot('low'), 'bg-info')
})

test('an unlabelled fact never falls into the low bucket', () => {
  // The list filters on this: "未标注" is its own bucket, because an unjudged
  // finding is not a low-risk one and must not be counted or shown as such.
  assert.equal(severityBucket(undefined), '')
  assert.equal(severityBucket(''), '')
  assert.equal(severityBucket('  '), '')
  assert.equal(severityBucket('High'), 'high')
  assert.notEqual(severityBucket(undefined), severityBucket('low'))
})
