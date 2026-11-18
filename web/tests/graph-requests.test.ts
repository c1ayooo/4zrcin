// The snapshot reader's request contract.
//
// The map shows the whole engagement, so one open reads the ledger to the end of
// the snapshot: there is no "load more" step to click and no partial first
// screen passed off as whole. These cases pin the read loop (cursor progression,
// snapshot fence, merge), the honesty of a partial read, and the bounded
// neighborhood read a deep link asks for.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import type { GraphRead } from '../src/lib/api.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let useGraphSnapshot: typeof import('../src/lib/graph-data.ts').useGraphSnapshot
let GRAPH_PAGE_LIMIT: number
let api: any
let graph: ReturnType<typeof useGraphSnapshot>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({
    root: fileURLToPath(new URL('..', import.meta.url)),
  })
  useGraphSnapshot = (await server.ssrLoadModule('/src/lib/graph-data.ts')).useGraphSnapshot
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  GRAPH_PAGE_LIMIT = (await server.ssrLoadModule('/src/lib/graph-pages.ts')).GRAPH_PAGE_LIMIT
})
afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})
after(async () => { await server?.close() })

function Harness() {
  graph = useGraphSnapshot(true, 'fixture')
  return null
}

/** One page of a fixed snapshot; a cursor means "there is another page". */
function page(seq: number, cursor = '', factIDs: string[] = [], over: Partial<GraphRead> = {}): GraphRead {
  return {
    facts: factIDs.map((id) => ({ id, intent_id: 'in_1', kind: 'note', title: id, summary: '', status: 'unverified', created_at: '2026-11-11T00:00:00Z' })),
    intents: [], goals: [], edges: [], boundary: [],
    total: factIDs.length,
    has_more: !!cursor, next_cursor: cursor,
    snapshot_version: { epoch: 'fixture', seq },
    ...over,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

const factIDs = (): string[] => (graph.data?.facts ?? []).map((fact) => fact.id)

async function mount(handler: (query: any) => Promise<GraphRead>) {
  api.graph = handler
  await act(async () => { renderer = create(React.createElement(Harness)) })
}

test('one open reads every page of the snapshot, then stops', async () => {
  const queries: any[] = []
  await mount(async (query) => {
    queries.push(query)
    if (!query.cursor) return page(1, 'cur_1', ['f_1'])
    return page(1, '', ['f_2'])
  })

  assert.deepEqual(queries.map((q) => q.cursor ?? null), [null, 'cur_1'], 'the cursor is followed to the end')
  assert.equal(queries[0].limit, GRAPH_PAGE_LIMIT, 'each page asks for the maximum row budget')
  assert.deepEqual(factIDs(), ['f_1', 'f_2'], 'pages of the same snapshot are merged')
  assert.equal(graph.data?.has_more, false, 'the snapshot is complete')
  assert.equal(graph.loading, false)
  assert.equal(graph.error, undefined)
  assert.equal('more' in graph, false, 'there is no load-more step to call')
})

test('a deep link is a bounded neighborhood read, not the whole ledger', async () => {
  const queries: any[] = []
  const intentRow = { id: 'in_1', title: 't', description: '', status: 'running', attempts: 1, created_at: '', updated_at: '' }
  await mount(async (query) => {
    queries.push(query)
    if (!query.root) return page(2, '', [])
    // A focus read that keeps handing out pages must still stop at the root.
    if (query.cursor) return page(2, 'cur_2', ['f_other'])
    return page(2, 'cur_1', ['f_root'], { intents: [intentRow] })
  })
  const before = queries.length
  await act(async () => { void graph.focus('intent:in_1') })
  const focusQueries = queries.slice(before)
  assert.equal(focusQueries[0].root, 'intent:in_1', 'the focus root is sent')
  assert.equal(focusQueries.length, 1, 'the read stops once the root is on hand')
  assert.deepEqual(factIDs(), ['f_root'])
  assert.equal(graph.error, undefined, 'a bounded neighborhood read is not a partial-read error')
})

test('a refresh fences a read that is still paging', async () => {
  const secondPage = deferred<GraphRead>()
  const freshPage = deferred<GraphRead>()
  let call = 0
  await mount(async () => {
    call += 1
    if (call === 1) return page(1, 'cur_old', ['f_old'])
    if (call === 2) return secondPage.promise
    return freshPage.promise
  })

  // The mount read is following its cursor when the operator refreshes.
  await act(async () => { graph.reload() })
  await act(async () => { freshPage.resolve(page(9, '', ['f_new'])) })
  assert.deepEqual(factIDs(), ['f_new'])

  await act(async () => { secondPage.resolve(page(1, '', ['f_stale'])) })
  assert.deepEqual(factIDs(), ['f_new'], 'a page of the replaced read cannot join the new snapshot')
  assert.equal(graph.data?.snapshot_version.seq, 9)
  assert.equal(graph.loading, false)
  assert.equal(graph.error, undefined)
})

test('a page that fails mid-read keeps the pages on hand and says the read is partial', async () => {
  let call = 0
  await mount(async () => {
    call += 1
    if (call === 1) return page(1, 'cur_1', ['f_1'])
    throw new Error('ledger unavailable')
  })
  assert.deepEqual(factIDs(), ['f_1'], 'the consistent prefix is kept')
  assert.equal(graph.data?.has_more, true, 'the snapshot is honestly marked incomplete')
  assert.equal(graph.error, 'ledger unavailable', 'the failure is reported, not swallowed')
  assert.equal(graph.loading, false)
})

test('a snapshot that moves between pages keeps the consistent prefix and reports it', async () => {
  let call = 0
  await mount(async () => {
    call += 1
    if (call === 1) return page(1, 'cur_1', ['f_1'])
    return page(2, '', ['f_other_snapshot'])
  })
  assert.deepEqual(factIDs(), ['f_1'], 'pages of two snapshots are never mixed')
  assert.equal(graph.data?.has_more, true)
  assert.match(String(graph.error), /快照未读完/)
})
