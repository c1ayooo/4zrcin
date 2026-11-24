// Settings surface: one tab stop for the tablist, autofill/input hints on the
// credential and address-like fields, and submit-time validation that marks the
// field instead of only showing a payload-level error.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import { installFormElementShim } from './helpers/dom.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let renderer: ReactTestRenderer | undefined
let SettingsPage: any, ConfirmProvider: any, api: any
const storage = new Map<string, string>()
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')

before(async () => {
  // The settings page now contains a Switch; Radix reads `HTMLFormElement`
  // directly, and react-test-renderer has no DOM to provide it.
  installFormElementShim()
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  SettingsPage = (await server.ssrLoadModule('/src/views/Settings.tsx')).SettingsPage
  ConfirmProvider = (await server.ssrLoadModule('@cyber/ui')).ConfirmProvider
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: { pathname: '/g/', search: '', hash: '' },
      sessionStorage: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) },
      addEventListener() {},
      removeEventListener() {},
      setInterval,
      clearInterval,
    },
  })
  Object.defineProperty(globalThis, 'document', { configurable: true, value: { visibilityState: 'visible', addEventListener() {}, removeEventListener() {} } })
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
  storage.clear()
})

after(async () => {
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  await server?.close()
})

async function mountSettings(saved: Record<string, unknown> = {}) {
  const settings = { base_url: 'http://gateway.test/v1', model: 'route', provider: 'openai', api_key_set: true, configured: true, ...saved }
  api.settings = async () => settings
  api.projectIndex = async () => ({ projects: [] })
  api.runtimeSettings = async () => ({ auxiliary_concurrency: 1, max_auxiliary_concurrency: 8, reviews_disabled: false })
  await act(async () => { renderer = create(React.createElement(ConfirmProvider, null, React.createElement(SettingsPage))) })
  return settings
}

function button(label: string) {
  return renderer!.root.findAllByType('button').find((node) => node.children.includes(label))!
}

test('the settings tablist has one tab stop and arrow keys move it with the panel', async () => {
  await mountSettings()
  const list = renderer!.root.findByProps({ role: 'tablist' })
  const tabs = () => renderer!.root.findAll((node) => node.type === 'button' && node.props.role === 'tab')
  assert.equal(tabs().length, 2)
  assert.deepEqual(tabs().map((node: any) => node.props['aria-selected']), [true, false])
  assert.deepEqual(tabs().map((node: any) => node.props.tabIndex), [0, -1])
  assert.equal(tabs()[0].props['aria-controls'], 'settings-panel-model')

  await act(async () => { list.props.onKeyDown({ key: 'ArrowRight', preventDefault() {} }) })
  assert.deepEqual(tabs().map((node: any) => node.props['aria-selected']), [false, true])
  assert.deepEqual(tabs().map((node: any) => node.props.tabIndex), [-1, 0])
  assert.equal(renderer!.root.findByProps({ id: 'settings-panel-model' }).props.hidden, true)
  assert.equal(renderer!.root.findByProps({ id: 'settings-panel-projects' }).props.hidden, false)

  await act(async () => { list.props.onKeyDown({ key: 'Home', preventDefault() {} }) })
  assert.deepEqual(tabs().map((node: any) => node.props['aria-selected']), [true, false])
})

test('connection fields carry the right autofill and input hints, and the key can be revealed', async () => {
  await mountSettings()
  const baseURL = renderer!.root.findByProps({ 'aria-label': '接口地址' })
  assert.equal(baseURL.props.autoComplete, 'url')
  assert.equal(baseURL.props.inputMode, 'url')
  assert.equal(baseURL.props.spellCheck, false)
  assert.equal(baseURL.props.enterKeyHint, 'next')
  assert.equal(renderer!.root.findByProps({ 'aria-label': '模型名称' }).props.autoComplete, 'off')
  const key = renderer!.root.findByProps({ 'aria-label': 'API Key' })
  assert.equal(key.props.type, 'password')
  assert.equal(key.props.autoComplete, 'new-password')
  await act(async () => { renderer!.root.findByProps({ 'aria-label': '显示 API Key' }).props.onClick() })
  assert.equal(renderer!.root.findByProps({ 'aria-label': 'API Key' }).props.type, 'text')
  assert.equal(renderer!.root.findByProps({ 'aria-label': '隐藏 API Key' }).props['aria-pressed'], true)
})

test('an invalid model-number draft is caught before the request and marked on the field', async () => {
  const writes: any[] = []
  api.saveSettings = async (body: any) => { writes.push(body); return { ...body, api_key_set: true, configured: true } }
  await mountSettings()
  const field = () => renderer!.root.findByProps({ 'aria-label': '上下文窗口' })
  await act(async () => { field().props.onChange({ target: { value: 'abc' } }) })
  await act(async () => { await button('保存并应用').props.onClick() })
  assert.equal(writes.length, 0, 'the invalid draft must not reach the API')
  assert.equal(field().props['aria-invalid'], true)
  assert.match(String(field().props['aria-describedby']), /-context_window-error$/)
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('上下文窗口必须是非负整数'))

  await act(async () => { field().props.onChange({ target: { value: '128000' } }) })
  assert.equal(field().props['aria-invalid'], undefined, 'editing the field clears its error state')
  await act(async () => { await button('保存并应用').props.onClick() })
  assert.equal(writes.length, 1)
  assert.equal(writes[0].context_window, 128000)
})

test('runtime concurrency rejects an out-of-range value and focuses the field', async () => {
  const writes: any[] = []
  api.saveRuntimeSettings = async (body: any) => { writes.push(body); return { auxiliary_concurrency: body.auxiliary_concurrency, max_auxiliary_concurrency: 8 } }
  await mountSettings()
  const field = () => renderer!.root.findByProps({ 'aria-label': '复核与报告并发' })
  assert.equal(field().props.required, true)
  assert.equal(field().props.inputMode, 'numeric')
  await act(async () => { field().props.onChange({ target: { value: '99' } }) })
  await act(async () => { await button('保存并发').props.onClick() })
  assert.deepEqual(writes, [])
  assert.equal(field().props['aria-invalid'], true)
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('1–8'))

  await act(async () => { field().props.onChange({ target: { value: '3' } }) })
  await act(async () => { await button('保存并发').props.onClick() })
  assert.deepEqual(writes, [{ auxiliary_concurrency: 3 }])
})
