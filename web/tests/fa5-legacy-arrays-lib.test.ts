// FA5 (task-17): ES2023 array-method migration of the pure ordering helpers.
//
// Every migrated site used to be `[...x].sort(cmp)` (a copy, then an in-place
// sort). `x.toSorted(cmp)` is specified as: take a fresh array, sort that, so
// the caller's array is never touched and the comparison order is identical.
// These tests pin both halves of that contract:
//   - the caller's array comes out in its original order (and a frozen input
//     would throw on any in-place mutation, since ESM runs in strict mode);
//   - the produced order equals the pre-migration reference implementation,
//     copied verbatim below, including comparator tie-breaks.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { sortReviewsNewestFirst } from '../src/lib/claim.ts'
import { groupRunHistory, runGroupKey, type RunGroup } from '../src/lib/run-history.ts'
import { orderReviewEntries, type ReviewEntry, type ReviewOrder } from '../src/lib/run-review.ts'
import { sortInstructions } from '../src/lib/instructions.ts'
import { breadcrumbPath, graphNodeOrder } from '../src/lib/graph-filter.ts'
import { buildGraph, stepID, type GraphEdge, type GraphNode } from '../src/graph/model.ts'
import type { Instruction, Intent, Review, Run } from '../src/lib/types.ts'

// ---- reference implementations: the exact pre-migration code ----

function refSortReviewsNewestFirst(reviews: Review[]): Review[] {
  return [...reviews].sort((a, b) => (b.created_at ?? '').localeCompare(a.created_at ?? ''))
}

function refGroupRunHistory(runs: readonly Run[]): RunGroup[] {
  const groups = new Map<string, RunGroup>()
  const ordered = [...runs].sort((a, b) =>
    b.started_at.localeCompare(a.started_at) || b.id.localeCompare(a.id),
  )
  for (const run of ordered) {
    const key = runGroupKey(run)
    const group = groups.get(key)
    if (group) group.runs.push(run)
    else groups.set(key, { key, runs: [run] })
  }
  return [...groups.values()]
}

function refOrderReviewEntries(entries: readonly ReviewEntry[], order: ReviewOrder): ReviewEntry[] {
  if (order === 'time') return [...entries]
  return [...entries].sort((a, b) => b.totalTokens - a.totalTokens || a.order - b.order)
}

function refSortInstructions(rows: Instruction[]): Instruction[] {
  return [...rows].sort((a, b) => {
    if ((a.state === 'active') !== (b.state === 'active')) return a.state === 'active' ? -1 : 1
    return b.updated_at.localeCompare(a.updated_at)
  })
}

function refGraphNodeOrder(nodes: readonly GraphNode[]): GraphNode[] {
  return [...nodes].sort((a, b) => {
    if (a.depth !== b.depth) return a.depth - b.depth
    if (a.y !== b.y) return a.y - b.y
    if (a.x !== b.x) return a.x - b.x
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
}

/** The pre-migration BFS breadcrumb (copy-then-sort of the parent list). */
function refBreadcrumbPath(
  model: Parameters<typeof breadcrumbPath>[0],
  id: string | null | undefined,
): GraphNode[] {
  if (!id) return []
  const byId = model.byId ?? new Map(model.nodes.map((node) => [node.id, node]))
  if (!byId.has(id)) return []
  if (byId.get(id)?.type === 'project') return [byId.get(id)!]
  const incoming = model.incoming ?? (() => {
    const index = new Map<string, string[]>()
    for (const edge of model.edges) {
      if (!byId.has(edge.source) || !byId.has(edge.target)) continue
      const list = index.get(edge.target)
      if (list) list.push(edge.source)
      else index.set(edge.target, [edge.source])
    }
    return index
  })()
  const downstream = new Map<string, string>()
  const seen = new Set<string>([id])
  const queue: string[] = [id]
  let projectID: string | null = null
  while (queue.length > 0 && !projectID) {
    const current = queue.shift()!
    const parents = [...(incoming.get(current) ?? [])].sort()
    for (const parent of parents) {
      if (seen.has(parent)) continue
      seen.add(parent)
      downstream.set(parent, current)
      if (byId.get(parent)?.type === 'project') {
        projectID = parent
        break
      }
      queue.push(parent)
    }
  }
  if (!projectID) return []
  const path: GraphNode[] = []
  let cursor: string | undefined = projectID
  while (cursor) {
    const node = byId.get(cursor)
    if (node) path.push(node)
    cursor = downstream.get(cursor)
  }
  return path
}

// ---- helpers ----

function review(id: string, created: string): Review {
  return { id, fact_id: 'f_1', verdict: 'pass', created_at: created }
}

function run(id: string, seconds: number, over: Partial<Run> = {}): Run {
  return {
    id,
    intent_id: 'in_a',
    node: 'local',
    status: 'done',
    events: 0,
    declared_facts: 0,
    started_at: new Date(Date.UTC(2026, 10, 10, 0, 0, seconds)).toISOString(),
    ...over,
  }
}

function instruction(id: string, state: 'active' | 'revoked', updated: string): Instruction {
  return {
    id,
    text: `instruction ${id}`,
    version: 1,
    state,
    created_by: 'operator',
    created_at: '2026-11-01T00:00:00Z',
    updated_at: updated,
  }
}

function entry(key: string, order: number, totalTokens: number): ReviewEntry {
  return {
    key,
    order,
    kind: 'model',
    label: '模型调用',
    state: 'completed',
    eventIndex: order,
    at: '2026-11-10T00:00:00Z',
    totalTokens,
  }
}

function node(id: string, over: Partial<GraphNode> = {}): GraphNode {
  return {
    id,
    type: 'fact',
    label: id,
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    depth: 1,
    ...over,
  }
}

function edge(source: string, target: string, kind: GraphEdge['kind'] = 'premise'): GraphEdge {
  return { id: `${source}->${target}:${kind}`, source, target, kind }
}

function intent(id: string, created: string, over: Partial<Intent> = {}): Intent {
  return {
    id,
    title: `intent ${id}`,
    description: '',
    status: 'done',
    attempts: 1,
    created_at: created,
    updated_at: created,
    ...over,
  }
}

function ids<T extends { id: string }>(rows: readonly T[]): string[] {
  return rows.map((row) => row.id)
}

// ---- claim reviews ----

test('claim review order matches copy-then-sort and leaves the caller array alone', () => {
  const reviews = [
    review('old', '2026-11-10T00:00:00Z'),
    review('new', '2026-11-12T00:00:00Z'),
    review('tie-a', '2026-11-11T00:00:00Z'),
    review('tie-b', '2026-11-11T00:00:00Z'),
  ]
  const before = ids(reviews)
  // Frozen input: an in-place sort would throw instead of silently reordering.
  const sorted = sortReviewsNewestFirst(Object.freeze([...reviews]) as Review[])
  assert.deepEqual(ids(sorted), ids(refSortReviewsNewestFirst(reviews)))
  assert.deepEqual(ids(sorted), ['new', 'tie-a', 'tie-b', 'old'])
  assert.deepEqual(ids(reviews), before)
  assert.notEqual(sorted, reviews)
})

// ---- run history grouping ----

test('run history grouping matches copy-then-sort and keeps the caller order', () => {
  const runs = [
    run('older', 0),
    run('newer', 20),
    run('same-second-b', 10),
    run('same-second-a', 10),
  ]
  const before = ids(runs)
  const groups = groupRunHistory(Object.freeze([...runs]) as Run[])
  const ref = refGroupRunHistory(runs)
  assert.deepEqual(groups.map((group) => group.key), ref.map((group) => group.key))
  assert.deepEqual(groups.map((group) => ids(group.runs)), ref.map((group) => ids(group.runs)))
  assert.deepEqual(ids(groups[0].runs), ['newer', 'same-second-b', 'same-second-a', 'older'])
  assert.deepEqual(ids(runs), before)
})

// ---- review entries (tokens / time) ----

function entryKeys(rows: readonly ReviewEntry[]): string[] {
  return rows.map((row) => row.key)
}

test('token ordering matches copy-then-sort and never reorders the entries', () => {
  const entries = [
    entry('cheap', 1, 10),
    entry('expensive', 2, 500),
    entry('tied-later', 4, 100),
    entry('tied-earlier', 3, 100),
  ]
  const before = entryKeys(entries)
  const ordered = orderReviewEntries(Object.freeze([...entries]) as ReviewEntry[], 'tokens')
  const ref = refOrderReviewEntries(entries, 'tokens')
  assert.deepEqual(entryKeys(ordered), entryKeys(ref))
  assert.deepEqual(entryKeys(ordered), ['expensive', 'tied-earlier', 'tied-later', 'cheap'])
  assert.deepEqual(entryKeys(entries), before)
  assert.notEqual(ordered, entries)
})

test('the time view is still a fresh copy in input order', () => {
  const entries = [entry('a', 1, 1), entry('b', 2, 2)]
  const byTime = orderReviewEntries(entries, 'time')
  assert.deepEqual(byTime, refOrderReviewEntries(entries, 'time'))
  assert.deepEqual(entryKeys(byTime), ['a', 'b'])
  assert.notEqual(byTime, entries)
})

// ---- persistent instructions ----

test('instruction order (active first, then newest) matches copy-then-sort', () => {
  const rows = [
    instruction('revoked-new', 'revoked', '2026-11-15T00:00:00Z'),
    instruction('active-old', 'active', '2026-11-01T00:00:00Z'),
    instruction('active-new', 'active', '2026-11-12T00:00:00Z'),
    instruction('revoked-old', 'revoked', '2026-11-02T00:00:00Z'),
  ]
  const before = ids(rows)
  const sorted = sortInstructions(Object.freeze([...rows]) as Instruction[])
  assert.deepEqual(ids(sorted), ids(refSortInstructions(rows)))
  assert.deepEqual(ids(sorted), ['active-new', 'active-old', 'revoked-new', 'revoked-old'])
  assert.deepEqual(ids(rows), before)
})

// ---- graph node order ----

test('graph node order matches copy-then-sort on depth/y/x/id and keeps the input list', () => {
  const nodes = [
    node('deep', { depth: 3 }),
    node('same-depth-high-y', { depth: 1, y: 10 }),
    node('same-depth-low-y', { depth: 1, y: -5 }),
    node('neg-x', { depth: 1, y: 0, x: -2 }),
    node('pos-x', { depth: 1, y: 0, x: 2 }),
    node('tie-b', { depth: 1, y: 0, x: 0 }),
    node('tie-a', { depth: 1, y: 0, x: 0 }),
  ]
  const before = ids(nodes)
  const ordered = graphNodeOrder(Object.freeze([...nodes]) as GraphNode[])
  assert.deepEqual(ids(ordered), ids(refGraphNodeOrder(nodes)))
  assert.deepEqual(ids(ordered), [
    'same-depth-low-y',
    'neg-x',
    'tie-a',
    'tie-b',
    'pos-x',
    'same-depth-high-y',
    'deep',
  ])
  assert.deepEqual(ids(nodes), before)
})

// ---- breadcrumb parents are sorted, never rewritten ----

test('breadcrumb BFS matches the copy-then-sort reference and leaves the incoming index intact', () => {
  const nodes = [
    node('p:proj', { type: 'project', depth: 0 }),
    node('f:a'),
    node('f:b'),
    node('s:in_a', { type: 'step', depth: 2 }),
    node('f:c', { depth: 3 }),
  ]
  // Several unsorted parents compete for the same BFS level; the sort decides.
  const incoming = new Map<string, string[]>([
    ['f:a', ['p:proj', 'f:b', 'f:c']],
    ['f:b', ['p:proj']],
    ['f:c', ['f:a']],
    ['s:in_a', ['f:a']],
  ])
  const snapshot = [...incoming.entries()].map(([key, list]) => [key, [...list]] as const)
  const model = { nodes, edges: [] as GraphEdge[], incoming }
  const path = breadcrumbPath(model, 'f:b')
  assert.deepEqual(path.map((row) => row.id), refBreadcrumbPath(model, 'f:b').map((row) => row.id))
  assert.deepEqual(path.map((row) => row.id), ['p:proj', 'f:b'])
  // The adjacency arrays are read-only inputs: no in-place sort may survive.
  assert.deepEqual([...incoming.entries()], snapshot)
  assert.deepEqual(
    incoming.get('f:a'),
    ['p:proj', 'f:b', 'f:c'],
    'the BFS parent list must be read, not sorted in place',
  )
})

test('breadcrumb without an incoming index sorts a copy of the parent list', () => {
  const nodes = [
    node('p:proj', { type: 'project', depth: 0 }),
    node('f:z'),
    node('f:a'),
    node('f:b'),
  ]
  const edges = [edge('p:proj', 'f:z'), edge('p:proj', 'f:a'), edge('f:z', 'f:b'), edge('f:a', 'f:b')]
  const model = { nodes, edges }
  const path = breadcrumbPath(model, 'f:b')
  assert.deepEqual(path.map((row) => row.id), refBreadcrumbPath(model, 'f:b').map((row) => row.id))
  assert.deepEqual(path.map((row) => row.id), ['p:proj', 'f:a', 'f:b'])
})

// ---- graph build: chronological intent order ----

test('buildGraph walks intents in the same chronological/id order without sorting the input', () => {
  const intents = [
    intent('in_late', '2026-11-12T02:00:00Z'),
    intent('in_early_b', '2026-11-12T01:00:00Z'),
    intent('in_early_a', '2026-11-12T01:00:00Z'),
    intent('in_first', '2026-11-12T00:00:00Z'),
  ]
  const before = ids(intents)
  const frozen = Object.freeze([...intents]) as Intent[]
  const model = buildGraph([], frozen, [])
  const refOrder = [...intents].sort((a, b) => {
    const at = a.created_at ?? ''
    const bt = b.created_at ?? ''
    if (at !== bt) return at < bt ? -1 : 1
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
  const stepIDs = model.nodes.filter((row) => row.type === 'step').map((row) => row.id)
  assert.deepEqual(stepIDs, refOrder.map((row) => stepID(row.id)))
  assert.deepEqual(stepIDs, ['s:in_first', 's:in_early_a', 's:in_early_b', 's:in_late'])
  assert.deepEqual(ids(intents), before)
})
