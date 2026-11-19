import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import { guardrailCallEventIndex } from '../src/lib/guardrail.ts'
import type { Event, GuardrailPending, GuardrailPendingItem } from '../src/lib/types.ts'

// ---- the call id is answered exactly, never approximately -------------------

function event(over: Partial<Event> = {}): Event {
  return { kind: 'tool_call', at: '2026-11-19T00:00:00Z', tool: 'shell.exec', ...over }
}

test('a call id matches exactly one journal event, in either recorded shape', () => {
  const events = [
    event({ call_id: 'call-a' }),
    event({ kind: 'guardrail_decision', guardrail_call_id: 'call-b', guardrail_action: 'review' }),
    event({ call_id: 'call-c' }),
  ]
  assert.equal(guardrailCallEventIndex(events, 'call-a'), 0)
  assert.equal(guardrailCallEventIndex(events, 'call-b'), 1)
  // Same id with padding still matches; a prefix or a different id does not.
  assert.equal(guardrailCallEventIndex(events, ' call-c '), 2)
  assert.equal(guardrailCallEventIndex(events, 'call'), -1)
  assert.equal(guardrailCallEventIndex(events, 'call-d'), -1)
  assert.equal(guardrailCallEventIndex(events, ''), -1)
  assert.equal(guardrailCallEventIndex(events, '   '), -1)
  assert.equal(guardrailCallEventIndex([], 'call-a'), -1)
})

// ---- the approval row offers the jump only when it can land ----------------

function item(over: Partial<GuardrailPendingItem> = {}): GuardrailPendingItem {
  return { operation_id: 'op_1', session_id: 'sess-abcdef1234567890', tool: 'browser.click', kind: 'tool', ...over }
}

function pendingPage(items: GuardrailPendingItem[]): GuardrailPending {
  return { state: 'available', checked_at: '', attempts: 1, count: items.length, pending: items }
}

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

function textOf(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string' || typeof value === 'number') return String(value)
  if (Array.isArray(value)) return value.map(textOf).join('')
  if (typeof value === 'object' && 'props' in (value as Record<string, unknown>)) {
    return textOf((value as { props?: { children?: unknown } }).props?.children)
  }
  return ''
}

function buttonWithText(text: string) {
  return renderer!.root.findAll(
    (node) => node.type === 'button' && textOf(node.props.children).includes(text),
  )[0]
}

test('a pending approval with a call id points at that exact call', async () => {
  api.guardrailPending = async () => pendingPage([item({ call_id: 'call-42' })])
  let target: { callID: string; sessionID: string } | undefined
  await act(async () => {
    renderer = create(
      React.createElement(GuardrailCard, { session: 'sess-abcdef1234567890', onLocateCall: (t: { callID: string; sessionID: string }) => { target = t } }),
    )
  })
  const locate = buttonWithText('在事件流里定位')
  assert.ok(locate, 'expected a locate button')
  await act(async () => { locate!.props.onClick() })
  assert.deepEqual(target, { callID: 'call-42', sessionID: 'sess-abcdef1234567890' })
})

test('without a call id the round stays approvable but not locatable', async () => {
  api.guardrailPending = async () => pendingPage([item({ call_id: undefined, arguments: { selector: '#login' } })])
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, { session: 'sess-abcdef1234567890', onLocateCall: () => {} }))
  })
  assert.equal(buttonWithText('在事件流里定位'), undefined)
  // Approving stays available: the missing id is not a reason to withhold the
  // decision, only to withhold the jump.
  assert.ok(buttonWithText('批准'))
  assert.ok(buttonWithText('拒绝'))
})

test('no owner means no jump: the row renders without the locate button', async () => {
  api.guardrailPending = async () => pendingPage([item({ call_id: 'call-42' })])
  await act(async () => {
    renderer = create(React.createElement(GuardrailCard, { session: 'sess-abcdef1234567890' }))
  })
  assert.equal(buttonWithText('在事件流里定位'), undefined)
})
