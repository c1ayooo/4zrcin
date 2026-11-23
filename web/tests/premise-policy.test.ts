import { test } from 'node:test'
import assert from 'node:assert/strict'
import { api } from '../src/lib/api.ts'
import { premisePolicyHint, premisePolicyLabel } from '../src/lib/format.ts'

function withFixture(
  run: (calls: { url: string; method: string; body?: unknown }[]) => Promise<void>,
  response: (calls: { url: string; method: string; body?: unknown }[]) => Response,
) {
  return async () => {
    const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
    const originalFetch = globalThis.fetch
    const calls: { url: string; method: string; body?: unknown }[] = []
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: { location: { pathname: '/p/fixture/' } },
    })
    globalThis.fetch = async (url, init) => {
      calls.push({
        url: String(url),
        method: init?.method ?? 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      })
      return response(calls)
    }
    try {
      await run(calls)
    } finally {
      globalThis.fetch = originalFetch
      if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
      else Reflect.deleteProperty(globalThis, 'window')
    }
  }
}

test(
  'premise policy is a project-scoped read, and the client has no writer for it',
  withFixture(
    async (calls) => {
      const read = await api.premisePolicy()
      assert.deepEqual(read, { premise_policy: 'verified_only', effective: 'verified_only' })
      // One mode, normalized on load: there is nothing to save, so the client
      // must not carry a save path that the server answers with 405.
      assert.equal((api as unknown as Record<string, unknown>).savePremisePolicy, undefined)
      assert.deepEqual(calls, [{ url: '/p/fixture/api/premise-policy', method: 'GET', body: undefined }])
    },
    () => new Response(JSON.stringify({ premise_policy: 'verified_only', effective: 'verified_only' })),
  ),
)

test('the fixed policy label and explanation describe the only mode', () => {
  assert.equal(premisePolicyLabel('verified_only'), '证明优先')
  assert.equal(premisePolicyLabel(undefined), '证明优先')
  assert.equal(premisePolicyLabel('something-else'), 'something-else')

  const verified = premisePolicyHint('verified_only')
  assert.match(verified, /必要前提/)
  assert.match(verified, /核验/)
  assert.match(verified, /复核/)
  // An unknown policy has no invented explanation.
  assert.equal(premisePolicyHint('something-else'), '')
})
