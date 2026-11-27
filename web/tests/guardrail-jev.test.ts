// The guardrail card's editable piece: the JEV judge's credentials. The saved
// key never echoes back, so the editor's contract is precise — an untouched
// key field must not reach the wire, a typed one rotates it, and removal is
// an explicit action.
import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import type { SettingsView } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let GuardrailPolicyCard: React.ComponentType<any>
let api: any
let renderer: ReactTestRenderer | undefined

function settingsView(over: Partial<SettingsView> = {}): SettingsView {
  return {
    base_url: 'http://gw/v1', model: 'm', provider: 'openai', api_key_set: true, configured: true,
    path: 'x', context_window: 0, max_tokens: 0,
    guardrail: { configured: true, provider: 'hybrid', jev_configured: true },
    jev: { configured: true, base_url: 'https://relay.example/v1', model: 'judge-1' },
    ...over,
  }
}

function asyncState(data: SettingsView): any {
  return {
    data, error: undefined, loading: false, refreshing: false, updatedAt: Date.now(),
    reload() {}, setData(next: SettingsView) { this.data = next }, revalidate() {},
  }
}

after(async () => {
  await act(async () => { renderer?.unmount() })
  await server?.close()
})

async function mount(data: SettingsView) {
  if (!server) {
    server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
    GuardrailPolicyCard = (await server.ssrLoadModule('/src/components/GuardrailPolicyCard.tsx')).GuardrailPolicyCard
    api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  }
  await act(async () => {
    renderer?.unmount()
    renderer = create(React.createElement(GuardrailPolicyCard, {
      guardrail: data.guardrail, jev: data.jev, settings: asyncState(data),
    }))
  })
  return JSON.stringify(renderer!.toJSON())
}

function input(label: string) {
  const node = renderer!.root.findAllByProps({ 'aria-label': label })
    .find((n) => typeof n.props.onChange === 'function')
  assert.ok(node, `${label} input missing`)
  return node
}

async function change(label: string, value: string) {
  await act(async () => { input(label).props.onChange({ target: { value } }) })
}

async function clickSave() {
  const button = renderer!.root.findAllByProps({ 'aria-label': '保存 JEV 凭据' })
    .find((n) => typeof n.props.onClick === 'function')
  assert.ok(button, 'save button missing')
  await act(async () => { button.props.onClick(); await Promise.resolve() })
}

test('a configured judge shows its status and never the key', async () => {
  const text = await mount(settingsView())
  assert.ok(text.includes('JEV 凭据已配置'), text)
  assert.ok(text.includes('留空保持不变'), text)
  assert.equal(input('JEV API Key').props.value, '')
  assert.equal(input('JEV Base URL').props.value, 'https://relay.example/v1')
  assert.equal(input('JEV 模型').props.value, 'judge-1')
})

test('typing a key rotates it; routing values ride along; the model half rides too', async () => {
  await mount(settingsView())
  const calls: any[] = []
  api.saveSettings = async (body: any) => {
    calls.push(body)
    return { ...settingsView(), jev: { ...body.jev_write, configured: true } }
  }
  await change('JEV API Key', 'tsk-new')
  await clickSave()
  assert.equal(calls.length, 1)
  assert.equal(calls[0].jev_write.api_key, 'tsk-new')
  assert.equal(calls[0].jev_write.base_url, 'https://relay.example/v1')
  assert.equal(calls[0].jev_write.model, 'judge-1')
  assert.equal(calls[0].jev_write.clear_api_key, undefined)
  // The PUT applies the whole settings view, so the model half rides along.
  assert.equal(calls[0].base_url, 'http://gw/v1')
  // The refreshed projection replaces the drafts and clears the key field.
  assert.equal(input('JEV API Key').props.value, '')
})

test('a save that did not touch the key sends no api_key at all', async () => {
  await mount(settingsView())
  const calls: any[] = []
  api.saveSettings = async (body: any) => { calls.push(body); return settingsView() }
  await change('JEV 模型', 'judge-2')
  await clickSave()
  assert.equal(calls.length, 1)
  assert.equal(calls[0].jev_write.api_key, undefined)
  assert.equal(calls[0].jev_write.model, 'judge-2')
  assert.ok(!JSON.stringify(calls[0]).includes('········'), 'the masked placeholder must never be sent as a value')
})

test('removal is an explicit action and sends the clear flag', async () => {
  await mount(settingsView())
  const calls: any[] = []
  api.saveSettings = async (body: any) => { calls.push(body); return settingsView({ jev: { configured: false } }) }
  const button = renderer!.root.findAllByProps({ 'aria-label': '清除 JEV 密钥' })
    .find((n) => typeof n.props.onClick === 'function')
  assert.ok(button, 'clear button missing (a configured judge must offer it)')
  await act(async () => { button.props.onClick(); await Promise.resolve() })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].jev_write.clear_api_key, true)
  assert.equal(calls[0].jev_write.api_key, undefined)
})

test('an unconfigured judge hides the clear button and an old server hides the editor', async () => {
  await mount(settingsView({ jev: { configured: false } }))
  const clear = renderer!.root.findAllByProps({ 'aria-label': '清除 JEV 密钥' })
  assert.ok(!clear.some((n) => typeof n.props.onClick === 'function'), 'nothing to clear')

  const text = await mount(settingsView({ jev: undefined }))
  assert.ok(!text.includes('JEV 凭据已配置'), 'no editor without a projection')
  assert.ok(text.includes('守门'), 'the policy summary itself still renders')
})
