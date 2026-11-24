import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import type { SettingsView } from '../src/lib/types.ts'

// The single traffic setting: the scan node's capture-retention cap. MiB in the
// form, bytes on the wire (traffic_body_retention_bytes). 0/absent is the
// generated default (2 GiB), never displayed as 0.

const GIB = 1024 * 1024 * 1024

let server: Awaited<ReturnType<typeof createTestServer>>
let TrafficRetentionPanel: React.ComponentType<any>
let api: any
let renderer: ReactTestRenderer | undefined

function settingsView(over: Partial<SettingsView> = {}): SettingsView {
  return {
    base_url: 'http://gw/v1', model: 'm', provider: 'openai', api_key_set: true, configured: true,
    path: 'x', context_window: 0, max_tokens: 0,
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
    TrafficRetentionPanel = (await server.ssrLoadModule('/src/views/Settings.tsx')).TrafficRetentionPanel
    api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  }
  await act(async () => {
    renderer?.unmount()
    renderer = create(React.createElement(TrafficRetentionPanel, { settings: asyncState(data) }))
  })
  return JSON.stringify(renderer!.toJSON())
}

function findInput() {
  return renderer!.root.findAllByProps({ 'aria-label': '捕获保留上限（MiB）' }).find((node) => typeof node.props.onChange === 'function')!
}

async function changeInput(value: string) {
  await act(async () => { findInput().props.onChange({ target: { value } }) })
}

async function clickSave() {
  const button = renderer!.root.findAllByProps({ 'aria-label': '保存捕获保留上限' }).find((node) => typeof node.props.onClick === 'function')!
  assert.ok(button, 'save button missing')
  await act(async () => { button.props.onClick(); await Promise.resolve() })
}

test('the default is shown as "默认 2 GiB（未显式设置）", not 0', async () => {
  const text = await mount(settingsView())
  assert.equal(findInput().props.value, '')
  assert.ok(text.includes('默认 2 GiB（未显式设置）'), text)
  assert.ok(text.includes('该节点上的项目共享这个上限'), text)
  assert.ok(text.includes('body_max_bytes'), text)
  assert.ok(text.includes('重启 scan 节点生效'), text)
})

test('an explicit value is shown in MiB and on the wire in bytes', async () => {
  const text = await mount(settingsView({ traffic_body_retention_bytes: 4 * GIB }))
  assert.equal(findInput().props.value, '4096')
  assert.ok(text.includes('已设置 4,096 MiB（4 GiB）'), text)

  const calls: any[] = []
  api.saveSettings = async (body: any) => {
    calls.push(body)
    return { ...settingsView(), traffic_body_retention_bytes: body.traffic_body_retention_bytes, applied: 'restart', requires_restart: true, restart_reason: 'traffic 只在 scan 启动时读取' }
  }
  await changeInput('8192')
  await clickSave()
  assert.equal(calls.length, 1)
  assert.equal(calls[0].traffic_body_retention_bytes, 8192 * 1024 * 1024)
  // The PUT applies the whole settings view, so the model half must ride along.
  assert.equal(calls[0].base_url, 'http://gw/v1')
  assert.equal(calls[0].model, 'm')
  const after = JSON.stringify(renderer!.toJSON())
  assert.ok(after.includes('已设置 8,192 MiB（8 GiB）'), after)
  assert.ok(after.includes('重启'), after)
})

test('invalid MiB values are rejected locally without a request', async () => {
  await mount(settingsView({ traffic_body_retention_bytes: 2 * GIB }))
  const calls: any[] = []
  api.saveSettings = async (body: any) => { calls.push(body); return settingsView({ ...body }) }
  await changeInput('8')
  await clickSave()
  assert.equal(calls.length, 0, 'an out-of-range value must not reach the server')
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('最小 16 MiB'))

  await changeInput('abc')
  await clickSave()
  assert.equal(calls.length, 0)
  assert.ok(JSON.stringify(renderer!.toJSON()).includes('保留上限必须是 MiB 整数'))
})
