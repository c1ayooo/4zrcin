import { test } from 'node:test'
import assert from 'node:assert/strict'
import { api } from '../src/lib/api.ts'

test('the auxiliary review/report concurrency is read and written on the instance endpoint', async () => {
  const originalFetch = globalThis.fetch
  const calls: { url: string; method: string; body?: unknown }[] = []
  globalThis.fetch = async (url, init) => {
    calls.push({
      url: String(url),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    })
    const value = calls.length === 1 ? 1 : 3
    return new Response(
      JSON.stringify({ auxiliary_concurrency: value, max_auxiliary_concurrency: 8, reviews_disabled: false }),
    )
  }
  try {
    const read = await api.runtimeSettings()
    assert.deepEqual(read, { auxiliary_concurrency: 1, max_auxiliary_concurrency: 8, reviews_disabled: false })
    const saved = await api.saveRuntimeSettings({ auxiliary_concurrency: 3 })
    assert.deepEqual(saved, { auxiliary_concurrency: 3, max_auxiliary_concurrency: 8, reviews_disabled: false })
    assert.deepEqual(calls, [
      { url: '/api/runtime-settings', method: 'GET', body: undefined },
      {
        url: '/api/runtime-settings',
        method: 'PUT',
        body: { auxiliary_concurrency: 3 },
      },
    ])
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('the review default is a field of the same request, sent alone when only it changes', async () => {
  const originalFetch = globalThis.fetch
  const calls: { method: string; body?: unknown }[] = []
  globalThis.fetch = async (_url, init) => {
    calls.push({ method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : undefined })
    return new Response(JSON.stringify({ auxiliary_concurrency: 1, max_auxiliary_concurrency: 8, reviews_disabled: true }))
  }
  try {
    const saved = await api.saveRuntimeSettings({ reviews_disabled: true })
    assert.equal(saved.reviews_disabled, true)
    // Omitted fields keep their stored value server-side: the body carries only
    // what changed, so one panel cannot overwrite the other.
    assert.deepEqual(calls, [{ method: 'PUT', body: { reviews_disabled: true } }])
  } finally {
    globalThis.fetch = originalFetch
  }
})
