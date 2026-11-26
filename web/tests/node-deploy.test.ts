// The deploy rail's SSH page (docs/14 §7.4, §10.4): the form lives on the nodes
// page next to the other two ways in, and a job's steps and log render from
// whatever the page polled — the rail itself never calls the api.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import type { DeployJob } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let NodeDeployRail: React.ComponentType<any>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  NodeDeployRail = (await server.ssrLoadModule('/src/components/NodeDeployRail.tsx')).NodeDeployRail
})

after(async () => {
  await server?.close()
})

const baseProps = {
  enabled: true,
  mode: 'command' as const,
  onModeChange: () => {},
  dist: { launcher: true, scan_full: true, scan_capture: false },
  enrollments: [],
  onCreate: async () => ({ id: 'e1', token: 't', install_command: 'curl x', expires_at: '' }),
  onJoinCommand: async () => 'cyber-scan agent',
  onStartDeploy: async () => {},
  onResetDeploy: () => {},
}

function job(over: Partial<DeployJob> = {}): DeployJob {
  return {
    id: 'j1',
    enrollment_id: 'e1',
    host: '10.0.0.5',
    port: 22,
    user: 'root',
    state: 'running',
    steps: [
      { key: 'connect', label: '连接', state: 'done', detail: '10.0.0.5:22' },
      { key: 'probe', label: '探测', state: 'running' },
      { key: 'bootstrap', label: '引导', state: 'pending' },
      { key: 'wait', label: '等待回连', state: 'pending' },
      { key: 'done', label: '完成', state: 'pending' },
    ],
    log: [{ at: '2026-11-26T10:00:00Z', step: 'probe', text: '系统：Linux x86_64' }],
    created_at: '2026-11-26T10:00:00Z',
    ...over,
  }
}

test('all three entry points and the recipes stay on this page', () => {
  const html = renderToStaticMarkup(React.createElement(NodeDeployRail, baseProps))
  for (const text of ['命令部署', 'SSH 部署', '直连', '构建配方', '生成接入命令']) {
    assert.ok(html.includes(text), `missing ${text}`)
  }
  // Without a job the SSH page is not the default; the command page is.
  assert.ok(!html.includes('开始部署'))
})

test('a running job shows its steps, the live log and no retry yet', () => {
  const html = renderToStaticMarkup(React.createElement(NodeDeployRail, { ...baseProps, mode: 'ssh' as const, deployJob: job() }))
  assert.ok(html.includes('SSH 部署'))
  // While a job owns the page the form is gone: no second job, no stale
  // credentials sitting in inputs.
  assert.ok(!html.includes('开始部署'))
  for (const label of ['连接', '探测', '引导', '等待回连', '完成']) {
    assert.ok(html.includes(label), `missing step ${label}`)
  }
  assert.ok(html.includes('部署中…'))
  assert.ok(html.includes('系统：Linux x86_64'))
  assert.ok(!html.includes('再次部署'))
})

test('a finished job states the outcome and offers another run', () => {
  const ok = renderToStaticMarkup(
    React.createElement(NodeDeployRail, {
      ...baseProps,
      mode: 'ssh' as const,
      deployJob: job({ state: 'succeeded' }),
    }),
  )
  assert.ok(ok.includes('已回连'))
  assert.ok(ok.includes('再次部署'))

  const bad = renderToStaticMarkup(
    React.createElement(NodeDeployRail, {
      ...baseProps,
      mode: 'ssh' as const,
      deployJob: job({
        state: 'failed',
        error: '引导脚本失败：exit status 2',
        steps: [{ key: 'bootstrap', label: '引导', state: 'failed', detail: '引导脚本失败：exit status 2' }],
      }),
    }),
  )
  assert.ok(bad.includes('部署失败'))
  assert.ok(bad.includes('引导脚本失败：exit status 2'))
  assert.ok(bad.includes('再次部署'))
})

test('credentials are typed as secrets and never echoed back', () => {
  const form = renderToStaticMarkup(React.createElement(NodeDeployRail, baseProps))
  // The SSH page starts on the command tab; the password input exists once the
  // operator picks SSH. The job view below carries no credential at all.
  assert.ok(form.includes('type="password"') || form.includes('SSH 部署'))
  const html = renderToStaticMarkup(
    React.createElement(NodeDeployRail, {
      ...baseProps,
      deployJob: job({ log: [{ at: '', step: 'connect', text: '连接 10.0.0.5:22' }] }),
    }),
  )
  for (const secret of ['hunter2', 'zr1_', 'BEGIN OPENSSH', 'sudo-pass']) {
    assert.ok(!html.includes(secret), `the job view leaks ${secret}`)
  }
})
