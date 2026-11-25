import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  guardrailActionLabel,
  guardrailActionVariant,
  guardrailArgumentsText,
  guardrailEventStateLabel,
  guardrailExpiry,
  guardrailItemTitle,
  guardrailResolveNotice,
  guardrailStateLabel,
  guardrailStateReason,
  guardrailStateVariant,
  parseGuardrailEvent,
} from '../src/lib/guardrail.ts'
import type { Event, GuardrailPending, GuardrailPendingItem } from '../src/lib/types.ts'

// ---- pure presentation rules -------------------------------------------------

function item(over: Partial<GuardrailPendingItem> = {}): GuardrailPendingItem {
  return { operation_id: 'op_1', session_id: 'sess-abcdef1234567890', tool: 'browser.click', kind: 'tool', ...over }
}

test('guardrail states distinguish never-checked, unavailable/not-enabled and available', () => {
  assert.equal(guardrailStateLabel('never_checked', 0), '未查询')
  assert.equal(guardrailStateLabel(undefined, 0), '未查询')
  assert.equal(guardrailStateLabel('unavailable', 0), '不可用')
  assert.equal(guardrailStateLabel('available', 0), '已查询，暂无待审批')
  assert.equal(guardrailStateLabel('available', 2), '2 项待审批')
  // The unavailable state must not be styled as an error: it is configuration.
  assert.equal(guardrailStateVariant('unavailable', 0), 'muted')
  assert.equal(guardrailStateVariant('never_checked', 0), 'outline')
  assert.equal(guardrailStateVariant('available', 0), 'success')
  assert.equal(guardrailStateVariant('available', 3), 'warning')
  assert.match(guardrailStateReason({ state: 'unavailable', reason: '宿主没有启用工具审批' }), /没有启用工具审批/)
  assert.match(guardrailStateReason({ state: 'never_checked' }), /还没有读取/)
})

test('pending item arguments and titles are rendered honestly', () => {
  assert.equal(guardrailItemTitle(item()), 'browser.click')
  assert.equal(guardrailItemTitle(item({ tool: '', kind: 'shell' })), 'shell')
  assert.equal(guardrailItemTitle(item({ tool: '', kind: '' })), '未命名工具调用')
  assert.equal(guardrailArgumentsText(item({ arguments: { selector: '#login', force: true } })), '{\n  "selector": "#login",\n  "force": true\n}')
  assert.equal(guardrailArgumentsText(item({ arguments_text: 'not-json-payload' })), 'not-json-payload')
  assert.equal(guardrailArgumentsText(item()), '（无参数）')
  const clipped = guardrailArgumentsText(item({ arguments_text: 'x'.repeat(50) }), 10)
  assert.equal(clipped.length, 11)
  assert.ok(clipped.endsWith('…'))
})

test('expiry never claims "expired" for a missing or invalid deadline', () => {
  const now = Date.parse('2026-11-18T00:00:00Z')
  assert.deepEqual(guardrailExpiry(undefined, now), { label: '无到期时间', expired: false, known: false })
  assert.deepEqual(guardrailExpiry('not-a-date', now), { label: '到期时间无效', expired: false, known: false })
  assert.deepEqual(guardrailExpiry('2026-11-18T00:00:30Z', now), { label: '剩余 30s', expired: false, known: true })
  assert.deepEqual(guardrailExpiry('2026-11-18T00:05:00Z', now), { label: '剩余 5 分钟', expired: false, known: true })
  assert.deepEqual(guardrailExpiry('2026-11-17T23:59:00Z', now), { label: '已过期', expired: true, known: true })
})

test('resolve feedback reports the host answer, including a 409 refusal', () => {
  assert.equal(guardrailResolveNotice({ state: 'resolved', session_id: 's', operation_id: 'o', approve: true, attempts: 1 }).tone, 'success')
  assert.match(guardrailResolveNotice({ state: 'resolved', session_id: 's', operation_id: 'o', approve: false, attempts: 1 }).text, /已拒绝/)
  const failed = guardrailResolveNotice({ state: 'failed', reason: '宿主没有这个待审批项', session_id: 's', operation_id: 'o', approve: true, attempts: 1 })
  assert.equal(failed.tone, 'warning')
  assert.match(failed.text, /宿主没有这个待审批项/)
  const unavailable = guardrailResolveNotice({ state: 'unavailable', reason: '没有可拨号的 AOP 宿主', session_id: 's', operation_id: 'o', approve: false, attempts: 1 })
  assert.equal(unavailable.tone, 'warning')
  assert.match(unavailable.text, /没有可拨号的 AOP 宿主/)
})

// ---- api wire contract -------------------------------------------------------

test('guardrail API is project-scoped and a 409 resolve answer stays a result', async () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  const calls: { url: string; method: string; body: unknown }[] = []
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/alpha/' } } })
  globalThis.fetch = async (url, init) => {
    calls.push({ url: String(url), method: init?.method ?? 'GET', body: init?.body })
    if (String(url).includes('/guardrail/resolve')) {
      return new Response(JSON.stringify({ state: 'failed', reason: '宿主没有这个待审批项', session_id: 's', operation_id: 'o', approve: true, attempts: 1 }), { status: 409 })
    }
    return new Response(JSON.stringify({ state: 'unavailable', reason: '宿主没有启用工具审批', checked_at: '2026-11-18T00:00:00Z', attempts: 1, count: 0, pending: [] }))
  }
  const { api } = await import('../src/lib/api.ts')
  try {
    const pending = await api.guardrailPending('sess-1')
    assert.equal(pending.state, 'unavailable')
    assert.equal(calls[0].url, '/p/alpha/api/guardrail/pending?session=sess-1')
    assert.equal(calls[0].method, 'GET')
    const result = await api.guardrailResolve({ session_id: 'sess-1', operation_id: 'op_1', approve: true })
    assert.equal(result.state, 'failed')
    assert.match(result.reason ?? '', /宿主没有这个待审批项/)
    assert.equal(calls[1].url, '/p/alpha/api/guardrail/resolve')
    assert.equal(calls[1].method, 'POST')
    assert.deepEqual(JSON.parse(String(calls[1].body)), { session_id: 'sess-1', operation_id: 'op_1', approve: true })
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})

// ---- guardrail run events ----------------------------------------------------

test('guardrail events parse the action from fields or from the status text', () => {
  const block = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail block bash：命令超出白名单',
    guardrail_action: 'block',
    guardrail_tool: 'bash',
    guardrail_reason: '命令超出白名单',
    guardrail_state: 'rejected',
  })
  assert.deepEqual(block, { action: 'block', tool: 'bash', reason: '命令超出白名单', state: 'rejected' })
  // Older rows may only carry the folded text.
  const textOnly = parseGuardrailEvent({ kind: 'guardrail_review', text: 'guardrail review browser.click：等待操作员' })
  assert.equal(textOnly?.action, 'review')
  assert.equal(textOnly?.tool, 'browser.click')
  assert.equal(textOnly?.reason, '等待操作员')
  assert.equal(parseGuardrailEvent({ kind: 'tool_call', text: 'guardrail block x' }), null)
  assert.equal(guardrailActionVariant('record'), 'muted')
  assert.equal(guardrailActionVariant('review'), 'warning')
  assert.equal(guardrailActionVariant('block'), 'danger')
  assert.equal(guardrailActionLabel('block'), '阻断')
  assert.equal(guardrailEventStateLabel('pending'), '待审批')
  assert.equal(guardrailEventStateLabel('expired'), '已过期')
  assert.equal(guardrailEventStateLabel(undefined), '')
})

test('the timeline renders guardrail decision/review rows with action badges and reasons', async () => {
  const { GuardrailEventRow } = await server.ssrLoadModule('/src/components/GuardrailEventRow.tsx')
  const decision: Event = {
    kind: 'guardrail_decision', at: '2026-11-18T00:00:00Z', text: 'guardrail block bash：命令超出白名单',
    guardrail_action: 'block', guardrail_tool: 'bash', guardrail_reason: '命令超出白名单', guardrail_state: 'rejected',
  }
  const review: Event = {
    kind: 'guardrail_review', at: '2026-11-18T00:00:01Z', text: 'guardrail review browser.click：等待操作员',
    guardrail_action: 'review', guardrail_tool: 'browser.click', guardrail_reason: '等待操作员',
    guardrail_state: 'pending', guardrail_operation_id: 'op_9', guardrail_call_id: 'call_9',
  }
  await act(async () => {
    renderer = create(React.createElement('div', null,
      React.createElement(GuardrailEventRow, { event: decision }),
      React.createElement(GuardrailEventRow, { event: review, sessionID: 'sess-run' }),
    ))
  })
  const text = rendered()
  assert.ok(text.includes('阻断'), text)
  assert.ok(text.includes('bash'), text)
  assert.ok(text.includes('命令超出白名单'), text)
  assert.ok(text.includes('复核'), text)
  assert.ok(text.includes('browser.click'), text)
  assert.ok(text.includes('待审批'), text)
  assert.ok(text.includes('查看审批状态'), text)
  assert.ok(text.includes('op_9'), text)

  // Without the run's session the pending review cannot be approved here.
  await act(async () => {
    renderer = create(React.createElement(GuardrailEventRow, { event: review }))
  })
  const readOnly = rendered()
  assert.ok(readOnly.includes('只读'), readOnly)
  assert.ok(!readOnly.includes('查看审批状态'), readOnly)
})

let server: Awaited<ReturnType<typeof createTestServer>>
let GuardrailCard: React.ComponentType<any>
let api: any
let renderer: ReactTestRenderer | undefined
const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')

before(async () => {
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { pathname: '/p/alpha/', search: '', hash: '' } } })
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GuardrailCard = (await server.ssrLoadModule('/src/components/GuardrailCard.tsx')).GuardrailCard
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => {
  await server?.close()
  if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
  else Reflect.deleteProperty(globalThis, 'window')
})

function rendered(): string {
  return JSON.stringify(renderer!.toJSON())
}

test('operator card shows "not enabled" state without the error colour or retry copy', async () => {
  api.guardrailPending = async () => ({
    state: 'unavailable',
    reason: '宿主没有启用工具审批（guardrail/JEV）：AOP guardrail 命名空间不可用',
    checked_at: '2026-11-18T00:00:00Z',
    attempts: 1,
    count: 0,
    pending: [],
  })
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, { summary: { state: 'unavailable', reason: '宿主没有启用工具审批（guardrail/JEV）：AOP guardrail 命名空间不可用', count: 0 } }))
  })
  const text = rendered()
  assert.ok(text.includes('工具审批'), text)
  assert.ok(text.includes('不可用'), text)
  assert.ok(text.includes('guardrail/JEV'), text)
  assert.ok(!text.includes('读取工具审批失败'), text)
})

test('an empty partial read does not say that the host has no pending approvals', async () => {
  api.guardrailPending = async () => ({
    state: 'available', count: 0, pending: [],
    sessions: [{ state: 'available' }, { state: 'unavailable' }],
  })
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard))
  })
  assert.match(rendered(), /部分会话未读到/)
  assert.match(rendered(), /已查到的会话暂无待审批，其余会话状态未知/)
  assert.doesNotMatch(rendered(), /这次查询未返回待审批项/)
})

test('a read that came back without a state keeps the summary reason instead of a second answer', async () => {
  // The card mounts with autoLoad, so it asks the host once on its own. A host
  // that answers without a usable state must not overwrite the summary: the
  // badge would say 不可用 while the line under it claimed nothing was read yet.
  api.guardrailPending = async () => ({ state: '', checked_at: '', attempts: 1, count: 0, pending: [] })
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, {
      summary: { state: 'unavailable', reason: '没有可用会话', count: 0 },
    }))
  })
  const text = rendered()
  assert.ok(text.includes('不可用'), text)
  assert.ok(text.includes('没有可用会话'), text)
  assert.ok(!text.includes('还没有读取过工具审批'), text)
})

test('pending rows render tool, session, expiry and arguments; approval targets the exact session', async () => {
  let seenSession = ''
  const pending: GuardrailPending = {
    state: 'available',
    checked_at: '2026-11-18T00:00:00Z',
    attempts: 1,
    count: 1,
    pending: [item({ tool: 'shell.exec', arguments: { command: 'id' }, expires_at: new Date(Date.now() + 30_000).toISOString() })],
  }
  api.guardrailPending = async (session: string) => { seenSession = session; return pending }
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, { session: 'sess-abcdef1234567890' }))
  })
  const text = rendered()
  assert.equal(seenSession, 'sess-abcdef1234567890')
  assert.ok(text.includes('1 项待审批'), text)
  assert.ok(text.includes('shell.exec'), text)
  assert.ok(text.includes('op_1'), text)
  assert.ok(text.includes('剩余'), text)
  assert.ok(text.includes('批准'), text)
  assert.ok(text.includes('拒绝'), text)
})

test('the run-page entry reads nothing until asked, then reads exactly that session', async () => {
  let reads = 0
  api.guardrailPending = async () => { reads++; return { state: 'available', checked_at: '', attempts: 1, count: 0, pending: [] } }
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, { session: 'sess-run', autoLoad: false }))
  })
  assert.equal(reads, 0)
  const text = rendered()
  assert.ok(text.includes('未查询'), text)
  assert.ok(text.includes('读取待审批项'), text)
  const load = renderer!.root.findAll((node) => node.type === 'button' && String(node.props.children ?? '').includes('读取待审批项'))[0]
    ?? renderer!.root.findAllByType('button').find((node) => JSON.stringify(node.props.children ?? '').includes('读取待审批项'))
  assert.ok(load, 'expected a load button')
  await act(async () => { load!.props.onClick() })
  assert.equal(reads, 1)
  assert.ok(rendered().includes('这次查询未返回待审批项'))
})

// ---- scope-rule verdicts ------------------------------------------------------

test('parseGuardrailEvent keeps the structured rule id and falls back to the reason token', () => {
  // The structured field wins (rows written after the field existed).
  const structured = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail review：未命中放行规则',
    guardrail_action: 'review',
    guardrail_rule_id: 'sr-2',
    guardrail_reason: '未命中放行规则',
  } as unknown as Event)
  assert.equal(structured?.ruleID, 'sr-2')
  assert.equal(structured?.reason, '未命中放行规则')

  // A row written before the structured field existed: the fork's leading
  // "rule=<id>" token is parsed out of the reason and removed from the display.
  const token = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail review：rule=sr-3 未命中放行规则，按默认动作挂起',
    guardrail_reason: 'rule=sr-3 未命中放行规则，按默认动作挂起',
  } as unknown as Event)
  assert.equal(token?.ruleID, 'sr-3')
  assert.equal(token?.reason, '未命中放行规则，按默认动作挂起')

  // A model judge's verdict has no rule; nothing is invented.
  const judge = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail block bash：策略判定',
    guardrail_action: 'block',
    guardrail_reason: '策略判定',
  } as unknown as Event)
  assert.equal(judge?.ruleID, undefined)
})

test('parseGuardrailEvent renders http.request verdicts as method + host + path', () => {
  const view = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail review http.request：POST https://api.target-corp.example/api/reset?x=1 未命中放行规则',
    guardrail_action: 'review',
    guardrail_tool: 'http.request',
    guardrail_rule_id: 'sr-2',
    guardrail_reason: 'POST https://api.target-corp.example/api/reset?x=1 未命中放行规则',
  } as unknown as Event)
  assert.deepEqual(view?.http, {
    method: 'POST',
    host: 'api.target-corp.example',
    path: '/api/reset?x=1',
    url: 'https://api.target-corp.example/api/reset?x=1',
  })
  assert.equal(view?.reason, '未命中放行规则')
  assert.equal(view?.ruleID, 'sr-2')

  // Not a URL-shaped reason: stays prose, no invented target.
  const prose = parseGuardrailEvent({
    kind: 'guardrail_decision',
    text: 'guardrail review http.request：无法解析目标',
    guardrail_tool: 'http.request',
    guardrail_reason: '无法解析目标',
  } as unknown as Event)
  assert.equal(prose?.http, undefined)
  assert.equal(prose?.reason, '无法解析目标')
})
