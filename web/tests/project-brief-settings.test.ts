import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { api } from '../src/lib/api.ts'
import {
  mergeProjectSettings,
  projectSettingsDraft,
  projectSettingsPatch,
} from '../src/lib/settings.ts'
import type { ProjectInfo } from '../src/lib/types.ts'

function project(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    id: 'p_example',
    name: '示例项目',
    scope: '评估 example.com 的外网门户',
    description: '只读优先',
    created: '2026-11-12T00:00:00Z',
    intents: 0,
    facts: 0,
    evidence: 0,
    running: 0,
    max_runs: null,
    max_runs_effective: 0,
    premise_policy: 'verified_only',
    ...over,
  }
}

test('the settings form no longer carries the brief or the constraint document', () => {
  const draft = projectSettingsDraft(project())
  // 起点 and 约束 are the project's documents, edited on the 约束 page with a
  // revision. The project endpoint answers 400 document_write_moved for them, so
  // the settings draft must not offer them at all.
  assert.deepEqual(Object.keys(draft).sort(), ['max_runs', 'round_timeout'])
  // The admission mode is a fixed project fact, not a draft field: it is not in
  // the settings form at all, so nothing can accidentally submit it.
  assert.equal((draft as unknown as Record<string, unknown>).premise_policy, undefined)
})

test('saving project settings never sends the brief or the constraint document', () => {
  const previous = projectSettingsDraft(project())
  assert.deepEqual(projectSettingsPatch(previous, previous), {})

  const changed = { ...previous, max_runs: '4', round_timeout: '15m' }
  assert.deepEqual(projectSettingsPatch(previous, changed), { max_runs: 4, round_timeout: '15m' })

  // Even a stale caller that still carries the retired fields cannot push them
  // back out: the patch only ever names settings.
  const stale = { ...previous, scope: '旧简报', description: '旧约束' } as unknown as typeof previous
  assert.deepEqual(projectSettingsPatch(previous, stale), {})

  // The single premise policy is never a draft change and never rides along,
  // even if a stale caller puts it in the draft: the server rejects the field.
  const legacy = { ...previous, premise_policy: 'verified_only' } as unknown as typeof previous
  assert.deepEqual(projectSettingsPatch(previous, legacy), {})
})

test('a ledger refresh preserves an edited setting while updating untouched fields', () => {
  const previous = projectSettingsDraft(project())
  const incoming = projectSettingsDraft(project({ max_runs: 8 }))
  const edited = { ...previous, max_runs: '3' }
  const merged = mergeProjectSettings(previous, incoming, edited)
  assert.equal(merged.max_runs, '3', 'an unsaved value is never clobbered by polling')
})

test('creating a project sends the operator-defined start and end, with no quantified criterion', async () => {
  const originalFetch = globalThis.fetch
  let sent: { url?: string; method?: string; body?: unknown } = {}
  globalThis.fetch = async (url, init) => {
    sent = {
      url: String(url),
      method: init?.method,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    }
    return new Response(
      JSON.stringify(project({ id: 'p-a1b2c3d4', name: '评估 example.com 的外网门户' })),
      { status: 201 },
    )
  }
  try {
    const created = await api.createProject(
      '评估 example.com 的外网门户',
      '起点：首页与登录口，已有一个测试账号',
      '终点：证明存在未授权访问并留下原始响应',
      '只读优先',
    )
    assert.equal(sent.url, '/api/projects')
    assert.equal(sent.method, 'POST')
    assert.deepEqual(sent.body, {
      name: '评估 example.com 的外网门户',
      scope: '起点：首页与登录口，已有一个测试账号',
      goal: '终点：证明存在未授权访问并留下原始响应',
      description: '只读优先',
    })
    assert.ok(!('quantified_target' in (sent.body as Record<string, unknown>)))
    // The directory id is decoupled from the name; the console navigates by id.
    assert.equal(created.id, 'p-a1b2c3d4')
    assert.equal(created.name, '评估 example.com 的外网门户')
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('project settings ask the server only for settings, and the brief is edited elsewhere', async () => {
  const originalFetch = globalThis.fetch
  let sent: { url?: string; method?: string; body?: unknown } = {}
  globalThis.fetch = async (url, init) => {
    sent = {
      url: String(url),
      method: init?.method,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    }
    return new Response(JSON.stringify(project({ max_runs: 8 })))
  }
  try {
    await api.updateProject('p_example', { max_runs: 8 })
    assert.equal(sent.url, '/api/projects/p_example')
    assert.equal(sent.method, 'PUT')
    // The retired scope/description fields would be answered 400
    // document_write_moved, so the settings save never names them.
    assert.deepEqual(sent.body, { max_runs: 8 })
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('no console page writes the brief or the constraint document through the project endpoint', async () => {
  const { readFileSync, readdirSync, statSync } = await import('node:fs')
  const { join } = await import('node:path')
  const root = fileURLToPath(new URL('../src', import.meta.url))
  const offenders: string[] = []
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry)
      if (statSync(path).isDirectory()) { walk(path); continue }
      if (!/\.(ts|tsx)$/.test(entry)) continue
      const source = readFileSync(path, 'utf8')
      // A settings save must not smuggle the two document fields back in.
      for (const match of source.matchAll(/updateProject\(([\s\S]{0,400}?)\)/g)) {
        if (/scope\s*:/.test(match[1]) || /description\s*:/.test(match[1])) offenders.push(path)
      }
    }
  }
  walk(root)
  assert.deepEqual(offenders, [], 'the brief and 约束 have one write entry: PUT /api/documents/{kind}')
  // The projects list keeps a read-only view and links to that page.
  const projects = readFileSync(join(root, 'views', 'Projects.tsx'), 'utf8')
  assert.match(projects, /\?view=docs/, 'the 起点/约束 dialog must link to the 约束 page')
  assert.doesNotMatch(projects, /saveScope/)
})

test('creating a goal sends the operator-defined start and end', async () => {
  const originalFetch = globalThis.fetch
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: { pathname: '/p/p_example/', search: '', hash: '' } },
  })
  let sent: { url?: string; method?: string; body?: unknown } = {}
  globalThis.fetch = async (url, init) => {
    sent = { url: String(url), method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined }
    return new Response(
      JSON.stringify({ id: 'g_1', statement: '终点', start: '起点', met: false, created_at: '2026-11-12T00:00:00Z' }),
      { status: 201 },
    )
  }
  try {
    await api.createGoal('终点', '起点')
    assert.equal(sent.url, '/p/p_example/api/goals')
    assert.equal(sent.method, 'POST')
    assert.deepEqual(sent.body, { statement: '终点', start: '起点' })
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})
