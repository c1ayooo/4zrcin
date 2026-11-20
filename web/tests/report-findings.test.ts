import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, api } from '../src/lib/api.ts'

/**
 * The report page reads the grades from the same deliverable file the operator
 * can download (`findings.json`), not from a second server-side summary. These
 * pin that path: the rows come back as an array, and a report whose body is
 * withheld surfaces the server's reason instead of an empty list.
 */
async function withFetch(respond: (url: string) => Response, fn: () => Promise<void>): Promise<void> {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/fixture/' } } })
  globalThis.fetch = async (url) => respond(String(url))
  try {
    await fn()
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
}

test('report findings come back as rows with their declared severity', async () => {
  await withFetch(
    (url) => {
      assert.equal(url, '/p/fixture/api/reports/rp_1/file?path=findings.json')
      return new Response(JSON.stringify([
        { fact: 'f1', verdict: 'confirmed', title: '配置暴露', severity: 'medium' },
      ]), { status: 200 })
    },
    async () => {
      const rows = await api.reportFindings('rp_1')
      assert.equal(rows.length, 1)
      assert.equal(rows[0].severity, 'medium')
      assert.equal(rows[0].title, '配置暴露')
    },
  )
})

test('a withheld report body is an error, not an empty list', async () => {
  await withFetch(
    () => new Response(JSON.stringify({ error: '报告校验未通过（incomplete）' }), { status: 409 }),
    async () => {
      await assert.rejects(
        () => api.reportFindings('rp_1'),
        (e: unknown) => {
          assert.ok(e instanceof ApiError)
          assert.equal(e.status, 409)
          assert.match(e.message, /incomplete/)
          return true
        },
      )
    },
  )
})
