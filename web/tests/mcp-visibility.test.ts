import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import type { SettingsView } from '../src/lib/types.ts'

// The MCP section's visibility model: unconfigured = disabled, and a configured
// server is visible to every session on the same scan node (a process-level
// command registry), not only to one role. These are the mental-model lines an
// operator needs before enabling MCP for the first time.

let server: Awaited<ReturnType<typeof createTestServer>>
let ExtensionsPanel: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

function settingsView(over: Partial<SettingsView> = {}): SettingsView {
  return {
    base_url: 'http://gw/v1', model: 'm', api_key_set: true, configured: true, path: 'x',
    mcp_servers: undefined, search_has_key: false, search_key_count: 0,
    ...over,
  }
}

function asyncState(data: SettingsView): any {
  return {
    data, error: undefined, loading: false, refreshing: false, updatedAt: Date.now(),
    reload() {}, setData() {}, revalidate() {},
  }
}

after(async () => {
  await act(async () => { renderer?.unmount() })
  await server?.close()
})

async function mount(data: SettingsView) {
  if (!server) {
    server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
    ExtensionsPanel = (await server.ssrLoadModule('/src/views/Settings.tsx')).ExtensionsPanel
  }
  await act(async () => {
    renderer?.unmount()
    renderer = create(React.createElement(ExtensionsPanel, { settings: asyncState(data) }))
  })
  return JSON.stringify(renderer!.toJSON())
}

test('configured MCP servers show their count and the node-wide visibility rule', async () => {
  const text = await mount(settingsView({
    mcp_servers: {
      playwright: { command: 'npx', args: ['-y', '@playwright/mcp'] },
      idamcp: { url: 'https://mcp.example/sse' },
    },
  }))
  assert.ok(text.includes('已配置 2 个 MCP 服务器'), text)
  assert.ok(text.includes('同一 scan 节点上的指挥官和执行者共享这些服务器'), text)
})

test('no MCP configuration is shown as disabled instead of an error', async () => {
  const text = await mount(settingsView())
  assert.ok(text.includes('MCP 未启用'), text)
  assert.ok(text.includes('同一 scan 节点上的指挥官和执行者共享这些服务器'), text)
  assert.ok(!text.includes('已配置 0 个'), text)
  assert.ok(!text.includes('text-destructive'), text)
})
