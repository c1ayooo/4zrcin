import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createServer } from 'vite'
import type { GraphRead } from '../src/lib/api.ts'

let server: Awaited<ReturnType<typeof createServer>>
let useGraphSnapshot: typeof import('../src/lib/graph-data.ts').useGraphSnapshot
let api: any
let graph: ReturnType<typeof useGraphSnapshot>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createServer({
    root: fileURLToPath(new URL('..', import.meta.url)),
    server: { middlewareMode: true, hmr: false },
    appType: 'custom',
  })
  useGraphSnapshot = (await server.ssrLoadModule('/src/lib/graph-data.ts')).useGraphSnapshot
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})
afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})
after(async () => { await server?.close() })

function Harness() {
  graph = useGraphSnapshot(true, 'fixture')
  return null
}

function page(seq: number, cursor = ''): GraphRead {
  return {
    facts: [], intents: [], goals: [], edges: [], boundary: [],
    total: 0, has_more: !!cursor, next_cursor: cursor,
    snapshot_version: { epoch: 'fixture', seq },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

async function mountWithMore() {
  // Six pages exhaust the initial batch while leaving a continuation cursor.
  api.graph = async () => page(1, 'old-cursor')
  await act(async () => { renderer = create(React.createElement(Harness)) })
  assert.equal(graph.data?.snapshot_version.seq, 1)
  assert.equal(graph.loading, false)
}

test('pagination cannot join an old snapshot while a replacement is loading', async () => {
  await mountWithMore()
  const replacement = deferred<GraphRead>()
  const oldPage = deferred<GraphRead>()
  const requests: any[] = []
  api.graph = (query: any) => {
    requests.push(query)
    return query.cursor ? oldPage.promise : replacement.promise
  }
  let more!: Promise<void>
  await act(async () => {
    graph.reload()
    more = graph.more()
  })
  await act(async () => { replacement.resolve(page(2)) })
  await act(async () => { oldPage.resolve(page(1)); await more })
  assert.equal(graph.data?.snapshot_version.seq, 2)
  assert.equal(requests.length, 1, 'reload must fence the old cursor immediately')
})

test('two pagination clicks in the same render share one pending request', async () => {
  await mountWithMore()
  const next = deferred<GraphRead>()
  let requests = 0
  api.graph = () => { requests++; return next.promise }
  let first!: Promise<void>, second!: Promise<void>
  await act(async () => {
    first = graph.more()
    second = graph.more()
  })
  assert.equal(requests, 1)
  await act(async () => { next.resolve(page(1)); await Promise.all([first, second]) })
  assert.equal(graph.busy, false)
})

test('late pagination cannot replace a refreshed snapshot or clear its request state', async () => {
  await mountWithMore()
  const next = deferred<GraphRead>()
  const replacement = deferred<GraphRead>()
  api.graph = (query: any) => query.cursor ? next.promise : replacement.promise
  let more!: Promise<void>
  await act(async () => { more = graph.more() })
  await act(async () => { graph.reload() })
  await act(async () => { next.resolve(page(1)); await more })
  assert.equal(graph.loading, true)
  await act(async () => { replacement.resolve(page(2)) })
  assert.equal(graph.data?.snapshot_version.seq, 2)
  assert.equal(graph.loading, false)
})
