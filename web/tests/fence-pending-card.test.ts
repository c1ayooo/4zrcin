// The pending-fence card's two halves: the api surface hits the project
// namespace with the exact resolve contract, and the list renders one row per
// queued asset with the decision buttons (so an approval here is visibly the
// same decision the API names).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import { api } from '../src/lib/api.ts'
import type { FencePendingAsset } from '../src/lib/types.ts'

function withProjectWindow<T>(pathname: string, fn: () => Promise<T>): Promise<T> {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname } } })
  const done = () => {
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
  return fn().finally(done)
}

function row(patch: Partial<FencePendingAsset>): FencePendingAsset {
  return {
    id: 'as_fixture',
    kind: 'domain',
    key: 'new.example',
    target: true,
    fence: 'pending',
    ...patch,
  }
}

test('the pending queue and its resolve call live in the project namespace', async () => {
  const originalFetch = globalThis.fetch
  const calls: { url: string; init?: RequestInit }[] = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url: String(url), init })
    if (String(url).endsWith('/api/fence-pending')) {
      return new Response(JSON.stringify({ pending: [row({})], count: 1 }))
    }
    return new Response(JSON.stringify({ results: [{ id: 'as_fixture', key: 'new.example', fence: 'in_fence' }] }))
  }
  try {
    await withProjectWindow('/p/fixture/', async () => {
      const list = await api.fencePending()
      assert.equal(list.count, 1)
      assert.equal(list.pending[0].key, 'new.example')
      const out = await api.resolveFencePending(['as_fixture'], true)
      assert.equal(out.results[0].fence, 'in_fence')
    })
    assert.deepEqual(
      calls.map((c) => c.url),
      ['/p/fixture/api/fence-pending', '/p/fixture/api/fence-pending/resolve'],
    )
    const body = JSON.parse(String(calls[1].init?.body))
    assert.deepEqual(body, { ids: ['as_fixture'], approve: true })
  } finally {
    globalThis.fetch = originalFetch
  }
})

let server: Awaited<ReturnType<typeof createTestServer>>
let FencePendingList: React.ComponentType<any>
let ConfirmProvider: React.ComponentType<any>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  const module = await server.ssrLoadModule('/src/components/FencePendingCard.tsx')
  FencePendingList = module.FencePendingList
  const ui = await server.ssrLoadModule('/packages/ui/src/confirm-dialog.tsx')
  ConfirmProvider = ui.ConfirmProvider
})

after(async () => {
  await server?.close()
})

function renderList(props: Record<string, unknown>): string {
  // useConfirm needs the app's provider, exactly like GlobalShell mounts it.
  return renderToStaticMarkup(
    React.createElement(ConfirmProvider, null, React.createElement(FencePendingList, props)),
  )
}

test('one row per queued asset, with both decisions on it', () => {
  const html = renderList({
    rows: [row({}), row({ id: 'as_ip', kind: 'host', key: '10.0.0.9' })],
  })
  assert.match(html, /待审资产（2）/)
  assert.match(html, /new\.example/)
  assert.match(html, /10\.0\.0\.9/)
  assert.match(html, /域名/)
  assert.match(html, /IP/)
  assert.match(html, /aria-label="待审资产"/)
  assert.match(html, /批准/)
  assert.match(html, /拒绝/)
  // Empty rows render an empty panel: "no queue, no card" is the container's
  // decision, not the list's.
  const empty = renderList({ rows: [] })
  assert.match(empty, /待审资产（0）/)
})

test('a read failure is a state with a retry, never an empty queue', () => {
  const html = renderList({ rows: [], error: '宿主不可达', onRetry: () => {} })
  assert.match(html, /读取失败：宿主不可达/)
  assert.match(html, /重试/)
})
