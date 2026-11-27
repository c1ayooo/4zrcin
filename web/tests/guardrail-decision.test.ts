// The guardrail card's decision editor: switching the judge (provider llm
// needs no JEV), the mode, the fail-open stance, and the llm judge subsection
// with the keep-on-empty key contract.
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

// Radix Select touches DOM globals in a mount effect; react-test-renderer has
// no DOM. The stub only has to exist — nothing in these tests reads it.
if (typeof (globalThis as any).HTMLFormElement === 'undefined') {
  (globalThis as any).HTMLFormElement = class HTMLFormElement {}
}

function settingsView(over: Partial<SettingsView> = {}): SettingsView {
  return {
    base_url: 'http://gw/v1', model: 'm', provider: 'openai', api_key_set: true, configured: true,
    path: 'x', context_window: 0, max_tokens: 0,
    guardrail: {
      configured: true, provider: 'none', mode: 'auto', on_error: 'review', shadow: false,
      scope: [], llm: { configured: false },
    },
    jev: { configured: false },
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

function select(label: string) {
  const trigger = renderer!.root.findAllByProps({ 'aria-label': label })
  assert.ok(trigger.length > 0, `${label} trigger missing`)
  return trigger[0]
}

function changeInput(label: string, value: string) {
  const node = renderer!.root.findAllByProps({ 'aria-label': label })
    .find((n) => typeof n.props.onChange === 'function')
  assert.ok(node, `${label} input missing`)
  return act(async () => { node.props.onChange({ target: { value } }) })
}

async function clickSave() {
  const button = renderer!.root.findAllByProps({ 'aria-label': '保存守门决策' })
    .find((n) => typeof n.props.onClick === 'function')
  assert.ok(button, 'save button missing')
  await act(async () => { button.props.onClick(); await Promise.resolve() })
}

test('the decision editor renders the stored decision and the llm judge opens only when selected', async () => {
  const text = await mount(settingsView({
    guardrail: { configured: false, provider: 'llm', mode: 'auto', llm: { configured: true, model: 'j', base_url: 'http://j/v1', api_key_set: true } },
  }))
  assert.ok(text.includes('裁判决策'), text)
  assert.ok(text.includes('llm — OpenAI 兼容模型'), text)
  assert.ok(text.includes('不需要 JEV'), 'the llm path must say it needs no JEV')
  // llm selected → the judge subsection is open.
  assert.ok(text.includes('裁判 Base URL'), text)
  assert.ok(text.includes('留空保持不变'), 'the key placeholder carries the rotation contract')
})

test('a none-judge deployment keeps the llm subsection closed', async () => {
  const text = await mount(settingsView())
  assert.ok(!text.includes('裁判 Base URL'), 'no judge connection fields for provider none')
})

test('saving sends the whole decision with the llm subsection; an untouched key is absent', async () => {
  await mount(settingsView({
    guardrail: { configured: true, provider: 'llm', mode: 'auto', on_error: 'review', shadow: false,
      llm: { configured: true, model: 'judge-1', base_url: 'http://j/v1' } },
  }))
  const calls: any[] = []
  api.saveSettings = async (body: any) => { calls.push(body); return settingsView() }
  await changeInput('LLM 裁判 API Key', 'sk-new')
  await clickSave()
  assert.equal(calls.length, 1)
  const write = calls[0].guardrail_decision_write
  assert.equal(write.provider, 'llm')
  assert.equal(write.mode, 'auto')
  assert.equal(write.shadow, false)
  assert.equal(write.llm.api_key, 'sk-new')
  assert.equal(write.llm.base_url, 'http://j/v1')
  assert.equal(write.llm.model, 'judge-1')
  assert.equal(calls[0].base_url, 'http://gw/v1', 'the model half rides along')
})
