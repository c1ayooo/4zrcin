import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, api } from '../src/lib/api.ts'

/**
 * A report body is only served for a report that passed its checks and still
 * matches the ledger. The refusal comes back as 409 with the reason in `error`;
 * the console has to show that reason, not a generic read failure, because the
 * reason *is* the gate ("still generating", "checks failed: fact_exists").
 */
async function withFetch(
  respond: (url: string) => Response,
  fn: () => Promise<void>,
): Promise<void> {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: { pathname: '/p/fixture/' } },
  })
  globalThis.fetch = async (url) => respond(String(url))
  try {
    await fn()
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
}

test('a withheld report body surfaces the reason the server gave, not "could not read"', async () => {
  const reason = '报告校验未通过（incomplete），正文与证据不对外提供，只能查看 checks.json；未通过的规则：fact_exists'
  await withFetch(
    (url) => {
      assert.equal(url, '/p/fixture/api/reports/rep-1/file?path=index.md')
      return new Response(JSON.stringify({ error: reason }), { status: 409 })
    },
    async () => {
      await assert.rejects(
        () => api.reportFileText('rep-1', 'index.md'),
        (e: unknown) => {
          assert.ok(e instanceof ApiError)
          assert.equal(e.status, 409)
          assert.equal(e.message, reason)
          return true
        },
      )
    },
  )
})

test('a deliverable report body is returned as text', async () => {
  await withFetch(
    () => new Response('# 报告正文\n', { status: 200 }),
    async () => {
      assert.equal(await api.reportFileText('rep-1', 'index.md'), '# 报告正文\n')
    },
  )
})

test('a refusal without a JSON body still names the path instead of staying silent', async () => {
  await withFetch(
    () => new Response('<html>gateway</html>', { status: 409 }),
    async () => {
      await assert.rejects(
        () => api.reportFileText('rep-1', 'index.md'),
        (e: unknown) => {
          assert.ok(e instanceof ApiError)
          assert.equal(e.status, 409)
          assert.equal(e.message, '无法读取 index.md')
          return true
        },
      )
    },
  )
})
