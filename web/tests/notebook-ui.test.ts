import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let Memory: React.ComponentType, renderer: ReactTestRenderer | undefined
let current: {state: string; version: number}, puts: {state: string; version: number}[]
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const originalFetch = globalThis.fetch

before(async () => {
  server = await createTestServer({root: fileURLToPath(new URL('..', import.meta.url))})
  Memory = (await server.ssrLoadModule('/src/views/Memory.tsx')).Memory
  Object.defineProperty(globalThis, 'window', {configurable: true, value: {location: {pathname: '/p/fixture/', search: '', hash: ''}}})
  globalThis.fetch = async (_url, init) => {
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body))
      puts.push(body)
      if (body.version !== current.version) return new Response(JSON.stringify({error: '版本已变化'}), {status: 409})
      current = {state: body.state, version: current.version + 1}
      return Response.json({saved: true, bytes: body.state.length, version: current.version, receipt: {version: current.version, writer: 'operator', sha256: 'fixture'}})
    }
    return Response.json({...current, path: 'state.md', receipt: {version: current.version, writer: 'operator', sha256: 'fixture'}})
  }
})
afterEach(async () => { await act(async () => { renderer?.unmount(); renderer = undefined }) })
after(async () => {
  globalThis.fetch = originalFetch
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  await server?.close()
})

function button(label: string) { return renderer!.root.findAllByType('button').find(node => node.children.filter(child => typeof child === 'string').join('') === label)! }
function editor() { return renderer!.root.findByType('textarea') }

test('a stale notebook save preserves the draft and requires reviewing the latest version before replacing it', async () => {
  current = {state: 'original judgement', version: 4}
  puts = []
  await act(async () => { renderer = create(React.createElement(Memory)) })
  await act(async () => { editor().props.onChange({target: {value: 'my judgement'}}) })
  current = {state: 'commander updated judgement', version: 5}
  await act(async () => { await button('保存').props.onClick() })
  assert.deepEqual(puts, [{state: 'my judgement', version: 4}])
  assert.equal(renderer!.root.findAllByType('textarea')[0].props.value, 'my judgement')
  assert.equal(renderer!.root.findAllByType('textarea')[1].props.value, 'commander updated judgement')
  assert.equal(button('保存').props.disabled, true)
  await act(async () => { button('保留草稿继续编辑').props.onClick() })
  assert.equal(editor().props.value, 'my judgement')
  await act(async () => { await button('保存').props.onClick() })
  assert.deepEqual(puts[1], {state: 'my judgement', version: 5})
  assert.equal(current.version, 6)
  assert.equal(button('保存').props.disabled, true)
})
