import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

// The boot quarantines a vuln index it cannot read: the file is renamed, the
// instance keeps running with an empty library. Empty searches would otherwise
// read as "the library has no entries", so the console must say what happened,
// where the file went, and that the ledger is untouched. Standing notice: no
// dismiss button, because the condition lasts until someone fixes the file.

let server: Awaited<ReturnType<typeof createTestServer>>
let Notice: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  Notice = (await server.ssrLoadModule('/src/views/Projects.tsx')).VulnLibraryQuarantineNotice
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => {
  await server.close()
})

async function render(quarantine: unknown) {
  await act(async () => {
    if (renderer) renderer.unmount()
    renderer = create(React.createElement(Notice, { quarantine }))
  })
  return renderer!
}

const text = (r: ReactTestRenderer) => JSON.stringify(r.toJSON())

test('a healthy boot renders nothing at all', async () => {
  assert.equal((await render(null)).toJSON(), null, 'null means this boot quarantined nothing')
  assert.equal((await render(undefined)).toJSON(), null, 'an absent field (older build) must not invent a notice')
})

test('a quarantined index names the file, the reason and the implication', async () => {
  const r = await render({
    path: 'D:/data/vulns/vulns.json.corrupt-20261124-151805',
    reason: 'parse vulns.json: unexpected end of JSON input',
    quarantined_at: '2026-11-24T15:18:05+08:00',
  })
  const body = text(r)
  for (const part of [
    '漏洞库索引损坏',
    'vulns.json.corrupt-20261124-151805',
    'unexpected end of JSON input',
    '不删、不截断',
    // The distinction that matters: empty search results are not empty data.
    '这不代表「库里没有漏洞」',
  ]) {
    assert.ok(body.includes(part), `notice is missing "${part}": ${body}`)
  }
})

test('the notice has no dismiss control: it stays until the file is fixed', async () => {
  const r = await render({
    path: '/data/vulns/vulns.json.corrupt-1',
    reason: 'parse error',
    quarantined_at: '2026-11-24T15:18:05+08:00',
  })
  const buttons = r.root.findAll((n) => typeof n.type === 'string' && (n.type === 'button' || n.type === 'a'))
  assert.equal(buttons.length, 0, `a standing notice must not offer dismiss/reopen: ${JSON.stringify(buttons.map((b) => b.type))}`)
})
