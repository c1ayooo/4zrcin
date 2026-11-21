// One direction's detail (lib/intent-detail.ts).
//
// These cases pin three rules the page must not soften: a premise the response
// did not include is missing (never implicitly verified), statuses come from the
// ledger as stored, and the rows keep the server's order instead of inventing a
// ranking. Counts are over the listed rows, and a truncated page says so.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { premiseBlockers, summarizeIntentFacts } from '../src/lib/intent-detail.ts'
import type { Fact } from '../src/lib/types.ts'

function fact(id: string, over: Partial<Fact> = {}): Fact {
  return {
    id,
    intent_id: 'in_access',
    kind: 'host',
    title: `FACT_${id}`,
    summary: '',
    confidence: 'observed',
    status: 'unverified',
    created_at: '2026-11-20T09:30:00+08:00',
    ...over,
  } as Fact
}

test('counts follow the stored statuses and sum only their own evidence', () => {
  const s = summarizeIntentFacts([
    fact('f_ok', { status: 'verified', evidence_ids: ['e_1', 'e_2'], claim_version: 3 }),
    fact('f_new', { status: 'unverified', evidence_ids: ['e_3'] }),
    fact('f_no', { status: 'rejected' }),
    fact('f_gone', { status: 'withdrawn', evidence_ids: [] }),
  ])
  assert.equal(s.total, 4)
  assert.equal(s.listed, 4)
  assert.equal(s.verified, 1)
  assert.equal(s.unverified, 1)
  assert.equal(s.closed, 2)
  assert.equal(s.evidence, 3)
  assert.equal(s.truncated, false)
  assert.equal(s.rows[0].status, '已复核')
  assert.equal(s.rows[2].status, '已否定')
  assert.equal(s.rows[3].status, '已撤回')
  assert.equal(s.rows[0].version, 3)
})

test('a hard premise keeps its premise role even when the direction declared it', () => {
  const s = summarizeIntentFacts(
    [fact('f_p', { status: 'verified' }), fact('f_d', { status: 'verified' })],
    { premiseIDs: ['f_p'] },
  )
  assert.deepEqual(s.rows.map((row) => row.role), ['premise', 'declared'])
  assert.equal(s.premises, 1)
})

test('the page keeps the server order instead of ranking facts itself', () => {
  const s = summarizeIntentFacts([fact('f_z'), fact('f_a'), fact('f_m')])
  assert.deepEqual(s.rows.map((row) => row.id), ['f_z', 'f_a', 'f_m'])
})

test('a premise missing from the response is reported missing, not assumed proven', () => {
  const s = summarizeIntentFacts([fact('f_p', { status: 'unverified' })], {
    premiseIDs: ['f_p', 'f_absent', 'f_absent'],
    total: 9,
  })
  assert.equal(s.premises, 1)
  assert.deepEqual(s.missingPremises, ['f_absent'])
  assert.equal(s.unverified, 1)
  assert.equal(s.verified, 0)
  assert.equal(s.total, 9)
  assert.equal(s.listed, 1)
  assert.equal(s.truncated, true)
})

test('an empty direction lists nothing and claims nothing', () => {
  const s = summarizeIntentFacts([], { premiseIDs: ['f_only_premise'] })
  assert.deepEqual(s.rows, [])
  assert.deepEqual(s.missingPremises, ['f_only_premise'])
  assert.equal(s.total, 0)
  assert.equal(s.evidence, 0)
})

test('premise blockers keep the backend reason and the openable id apart', () => {
  // The board appends the reason: `"<id>（<reason>）"`. A link must open the id,
  // and a fact that does not exist must not become a link.
  assert.deepEqual(
    premiseBlockers({
      id: 's_1',
      title: 't',
      ready: false,
      blocked_on: ['f_a（已撤回）', 'f_a（已撤回）', 'f_gone（不存在）', ''],
      premise_changed: ['f_b', 'f_b'],
    }),
    {
      blocked: [{ id: 'f_a', reason: '已撤回' }, { id: 'f_gone', reason: '不存在' }],
      changed: ['f_b'],
    },
  )
  assert.deepEqual(premiseBlockers({ id: 's_2', title: 't', ready: true }), { blocked: [], changed: [] })
  assert.deepEqual(premiseBlockers(undefined), { blocked: [], changed: [] })
  // A bare id (older board rows) still parses, with no reason.
  assert.deepEqual(premiseBlockers({ id: 's_3', title: 't', ready: false, blocked_on: ['f_plain'] }).blocked, [
    { id: 'f_plain', reason: '' },
  ])
})
