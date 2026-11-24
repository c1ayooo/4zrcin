// The F13 regression (docs/35 §4 / docs/handoff §5 F13): the graph model is
// cached by a signature, so the signature has to cover every field the map and
// its inspectors actually render — not just row identities and status. These
// tests pin the two halves of the rule:
//   * a correction that leaves status unchanged (title, summary/claim version,
//     goal note/from, review state) must rebuild the model;
//   * a poll that returns the same data must reuse the same model object, or the
//     canvas re-lays out and the operator's pan/zoom jump on every poll.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  defaultGraphFilters,
  graphModelSignature,
  resolveGraphModelMemo,
  type GraphModelMemo,
  type GraphModelSource,
} from '../src/lib/graph-model-cache.ts'
import type { GraphRead } from '../src/lib/api.ts'
import type { Fact, Goal, Intent } from '../src/lib/types.ts'

const NOW = Date.parse('2026-11-12T12:00:00Z')

function fact(id: string, over: Partial<Fact> = {}): Fact {
  return {
    id,
    intent_id: 'in_task',
    kind: 'note',
    title: id,
    summary: '',
    confidence: 'observed',
    status: 'unverified',
    claim_version: 1,
    created_at: '2026-11-12T00:00:00Z',
    ...over,
  }
}

function intent(over: Partial<Intent> = {}): Intent {
  return {
    id: 'in_task',
    title: 'task',
    description: '',
    status: 'running',
    attempts: 1,
    created_at: '2026-11-12T00:00:00Z',
    updated_at: '2026-11-12T00:00:00Z',
    ...over,
  }
}

function goal(over: Partial<Goal> = {}): Goal {
  return {
    id: 'g_1',
    statement: '拿到域管',
    met: true,
    created_at: '2026-11-12T00:00:00Z',
    ...over,
  }
}

/** One fresh object graph per call, like a poll response: same content, new
 *  arrays/objects. Reference identity must not be what keeps the cache warm. */
function source(
  facts: Fact[],
  intents: Intent[] = [intent()],
  goals: Goal[] = [],
  extra: Partial<GraphRead> = {},
): GraphModelSource {
  const data: GraphRead = {
    facts,
    intents,
    goals,
    edges: [],
    boundary: [],
    has_more: false,
    total: facts.length + intents.length + goals.length,
    snapshot_version: { epoch: 'e1', seq: 1 },
    ...extra,
  }
  return { data, runs: [], defaultModel: 'model-a' }
}

function resolve(prev: GraphModelMemo | null, src: GraphModelSource): GraphModelMemo {
  return resolveGraphModelMemo(prev, src, defaultGraphFilters(), NOW)
}

// --- unchanged poll -------------------------------------------------------

test('an identical poll produces the same signature and reuses the same model object', () => {
  const facts = [fact('f_1', { title: '域控', summary: '普通域用户可读' }), fact('f_2')]
  const goals = [goal({ note: '证据 e_1 复现', from: ['f_1'], evidence_id: 'e_1' })]
  const first = resolve(null, source(facts, [intent()], goals))
  // A later poll: fresh object graph, identical content.
  const second = resolve(first, source(
    [fact('f_1', { title: '域控', summary: '普通域用户可读' }), fact('f_2')],
    [intent()],
    [goal({ note: '证据 e_1 复现', from: ['f_1'], evidence_id: 'e_1' })],
  ))
  assert.equal(second.sig, first.sig)
  assert.equal(second.model, first.model, 'no re-layout on an unchanged poll')
})

test('the signature is pure over content: a second call on the same data is identical', () => {
  const src = source([fact('f_1', { title: '标题' })], [intent({ title: '意图' })], [goal({ from: ['f_1'] })])
  const inputs = { facts: src.data!.facts, intents: src.data!.intents, goals: src.data!.goals }
  assert.equal(graphModelSignature(src, inputs), graphModelSignature(src, inputs))
})

test('changing the initiator refreshes task details without changing task ownership', () => {
  const first = resolve(null, source([], [intent({ parent_intent_id: 'in_parent', created_by_participant_id: 'commander' })]))
  const second = resolve(first, source([], [intent({ parent_intent_id: 'in_parent', created_by_participant_id: 'pt_in_parent' })]))
  assert.notEqual(second.sig, first.sig)
  assert.notEqual(second.model, first.model)
})

// --- fact corrections that keep the same status --------------------------

test('a corrected fact title rebuilds the model although the status did not change', () => {
  const first = resolve(null, source([fact('f_1', { title: '旧标题', status: 'unverified' })]))
  const second = resolve(first, source([fact('f_1', { title: '更正后的标题', status: 'unverified' })]))
  assert.notEqual(second.sig, first.sig)
  assert.notEqual(second.model, first.model, 'a retitled fact must rebuild the model')
  assert.equal(second.model.byId.get('f:f_1')?.label, '更正后的标题')
})

test('a corrected summary (claim version bumped) rebuilds the model with the same status', () => {
  const before = fact('f_1', { title: '暴露面', summary: '旧摘要', claim_version: 1, status: 'unverified' })
  const after = fact('f_1', { title: '暴露面', summary: '更正后的摘要', claim_version: 2, status: 'unverified' })
  const first = resolve(null, source([before]))
  const second = resolve(first, source([after]))
  assert.notEqual(second.sig, first.sig)
  assert.notEqual(second.model, first.model)
  assert.equal(second.model.byId.get('f:f_1')?.sub, '更正后的摘要')
  assert.equal(second.model.factsById.get('f_1')?.claim_version, 2)
})

test('a claim version bump alone rebuilds: the version is a real record identity', () => {
  const first = resolve(null, source([fact('f_1', { claim_version: 1 })]))
  const second = resolve(first, source([fact('f_1', { claim_version: 2 })]))
  assert.notEqual(second.sig, first.sig, 'a new assertion version is not a cache hit')
})

test('a legacy fact without a claim version falls back to hashing its claim text', () => {
  const legacy = (summary: string): Fact =>
    fact('f_1', { summary, claim_version: 0, title: '旧事实' })
  const first = resolve(null, source([legacy('旧摘要')]))
  const second = resolve(first, source([legacy('旧摘要')]))
  assert.equal(second.model, first.model, 'same legacy text is still a cache hit')
  const third = resolve(second, source([legacy('旧摘要（更正）')]))
  assert.notEqual(third.sig, second.sig)
  assert.notEqual(third.model, second.model)
})

test('a review-state change (fact_meta) rebuilds and reaches the fact node', () => {
  const first = resolve(null, source([fact('f_1')]))
  const second = resolve(first, source([fact('f_1')], [intent()], [], {
    fact_meta: [{ id: 'f_1', needs_review: true, reviewed_claim_version: 1, evidence_count: 2, ref_count: 1 }],
  }))
  assert.notEqual(second.sig, first.sig)
  const node = second.model.byId.get('f:f_1')
  assert.equal(node?.needsReview, true)
  assert.equal(node?.evidenceCount, 2)
  assert.equal(node?.refCount, 1)
})

test('an identical repeat of the reviewed fact_meta keeps the model warm', () => {
  const withMeta = () =>
    source([fact('f_1')], [intent()], [], {
      fact_meta: [{ id: 'f_1', needs_review: false, reviewed_claim_version: 1, evidence_count: 0, ref_count: 0 }],
    })
  const first = resolve(null, withMeta())
  const second = resolve(first, withMeta())
  assert.equal(second.model, first.model)
})

// --- goal corrections ----------------------------------------------------

test('a goal note change rebuilds the model although met did not change', () => {
  const sealed = (note: string, from: string[]) =>
    goal({ note, from, evidence_id: 'e_1', completed_at: '2026-11-12T01:00:00Z' })
  const first = resolve(null, source([fact('f_1')], [intent()], [sealed('理由 A', ['f_1'])]))
  const second = resolve(first, source([fact('f_1')], [intent()], [sealed('理由 B', ['f_1'])]))
  assert.notEqual(second.sig, first.sig)
  assert.notEqual(second.model, first.model)
  assert.equal(second.model.byId.get('g:g_1')?.meta?.goal?.note, '理由 B')
})

test('a goal from-list change rebuilds the model and keeps the goal listed as met', () => {
  const sealed = (from: string[]) => goal({ note: '依据变化', from })
  const first = resolve(null, source([fact('f_1'), fact('f_2')], [intent()], [sealed(['f_1'])]))
  const second = resolve(first, source([fact('f_1'), fact('f_2')], [intent()], [sealed(['f_1', 'f_2'])]))
  assert.notEqual(second.sig, first.sig)
  assert.notEqual(second.model, first.model)
  const node = second.model.byId.get('g:g_1')
  assert.equal(node?.status, 'met')
  assert.deepEqual(node?.meta?.goal?.from, ['f_1', 'f_2'])
})

// --- server omission rows (details) ---------------------------------------

test('a details row that omits a direction field rebuilds the model', () => {
  const first = resolve(null, source([], [intent()]))
  assert.ok(
    first.model.edges.some((e) => e.source === 'p:project' && e.target === 's:in_task' && e.kind === 'premise'),
    'without an omission row a direction with no premise starts at the brief',
  )
  const second = resolve(first, source([], [intent()], [], {
    details: [{ id: 'intent:in_task', query: 'intent:in_task', omitted: ['from_fact_ids'] }],
  }))
  assert.notEqual(second.sig, first.sig, 'omitted fields are part of what the model reads')
  assert.notEqual(second.model, first.model)
  // sourcesOf returns only the context list when the server omitted the premise
  // field: an empty selection no longer falls back to the project brief, and the
  // model must not invent a premise edge either way.
  assert.ok(
    !second.model.edges.some((e) => e.target === 's:in_task' && e.kind === 'premise'),
    'the omitted premise list is not guessed from the project brief',
  )
})

// --- boundary state is rendered state too --------------------------------
test('a boundary reference changing from not_loaded to missing rebuilds with an honest label', () => {
  const child = intent({ id: 'in_child', from_fact_ids: ['old'] })
  const page = (state: string): GraphRead => ({
    facts: [],
    intents: [child],
    goals: [],
    edges: [],
    boundary: [{ id: 'fact:old', state }],
    has_more: false,
    total: 1,
    snapshot_version: { epoch: 'e1', seq: 1 },
  })
  const first = resolve(null, { ...source([], [child]), data: page('not_loaded') })
  const second = resolve(first, { ...source([], [child]), data: page('missing') })
  assert.notEqual(second.sig, first.sig, 'the boundary label state is part of the key')
  assert.notEqual(second.model, first.model)
  assert.match(second.model.byId.get('f:old')?.label ?? '', /引用缺失/)
  assert.equal(second.model.byId.get('f:old')?.boundaryState, 'missing')
})

// --- positions survive a rebuild -----------------------------------------

test('closing a previously completed goal invalidates the graph cache and removes completion links',()=>{
 const facts=[fact('f_1')]
 const initial=resolve(null,source(facts,[],[goal({from:['f_1']})]))
 const closed=resolve(initial,source(facts,[],[goal({from:['f_1'],closed:true,closed_reason:'withdrawn'})]))
 assert.notEqual(closed.sig,initial.sig)
 assert.ok(initial.model.nodes.some(n=>n.id==='g:g_1'))
 assert.ok(!closed.model.nodes.some(n=>n.id==='g:g_1'))
 assert.ok(!closed.model.edges.some(l=>l.from==='g:g_1'||l.to==='g:g_1'))
})

// --- positions survive a rebuild -----------------------------------------

/**
 * The force layout writes its settled x/y into the node objects it was handed, so
 * a rebuilt model is the only thing that could move the picture. A poll that
 * corrects one fact rebuilds the model; before the carry it handed back fresh
 * nodes at the origin, the map jumped under the pointer and the next click landed
 * on empty space. Pin the rule: a rebuild keeps every surviving node where it was.
 */
test('a rebuild caused by a correction keeps surviving nodes where they were', () => {
  const facts = [fact('f_1'), fact('f_2'), fact('f_3')]
  const first = resolve(null, source(facts, [intent()]))
  // Settle: the sim writes into the very node objects the model holds.
  first.model.nodes.forEach((node, i) => {
    node.x = 100 + i * 10
    node.y = 50 + i * 7
    node.vx = 2
    node.vy = -3
  })
  const placed = new Map(first.model.nodes.map((node) => [node.id, { x: node.x, y: node.y }]))
  const corrected = resolve(
    first,
    source([fact('f_1', { title: '更正后的标题' }), fact('f_2'), fact('f_3')], [intent()]),
  )
  assert.notEqual(corrected.sig, first.sig, 'a correction is a rebuild, not a cache hit')

  const now = new Map(corrected.model.nodes.map((node) => [node.id, node]))
  let compared = 0
  for (const node of first.model.nodes) {
    const next = now.get(node.id)
    if (!next) continue
    const before = placed.get(node.id)!
    assert.deepEqual({ x: next.x, y: next.y }, before, `${node.id} must not move on a rebuild`)
    assert.equal(next.vx, 0, 'a carried node must not start with leftover velocity')
    assert.equal(next.vy, 0)
    compared += 1
  }
  assert.ok(compared > 1, 'the rebuild keeps more than one node')
})
