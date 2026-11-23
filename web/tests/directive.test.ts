// One 下发 box: recipient + text, nothing else.
//
// The console dropped the lifetime switch and the authorization checkbox when
// boundaries moved into the project documents. These cases pin what a directive
// is now: one durable message, a receipt that says where it went, and no claim
// that it changed any boundary.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, api } from '../src/lib/api.ts'
import {
  directiveErrorMessage,
  directiveFingerprint,
  directiveKeyFor,
  directiveReceipt,
  directiveRequest,
  isDirectionFinished,
  oneShotDirections,
  runningDirectionsHint,
  type DirectiveDraft,
} from '../src/lib/directive.ts'
import type { DirectiveResponse, Intent } from '../src/lib/types.ts'

function draft(over: Partial<DirectiveDraft> = {}): DirectiveDraft {
  return {
    text: '下一步先补 /api 的授权模型',
    target: 'commander',
    intentID: '',
    ...over,
  }
}

const directions = [
  { id: 'in_running', title: '在跑意图', status: 'running' },
  { id: 'in_pending', title: '待办意图', status: 'pending' },
  { id: 'in_done', title: '完成意图', status: 'done' },
  { id: 'in_failed', title: '失败意图', status: 'failed' },
  { id: 'in_parked', title: '停放意图', status: 'parked' },
] as Intent[]

test('the request builder sends one durable message and nothing about a lifetime', () => {
  assert.deepEqual(directiveRequest(draft(), 'key-1'), {
    text: '下一步先补 /api 的授权模型',
    target: 'commander',
    persistent: false,
    steer: false,
    idempotency_key: 'key-1',
  })
  assert.deepEqual(
    directiveRequest(draft({ target: 'intent', intentID: 'in_x' }), 'key-2'),
    {
      text: '下一步先补 /api 的授权模型',
      target: 'intent',
      intent_id: 'in_x',
      persistent: false,
      steer: false,
      idempotency_key: 'key-2',
    },
  )
  // Text is trimmed; intent ids are whitespace-normalised.
  assert.equal(directiveRequest(draft({ text: '  hi  ' }), 'k').text, 'hi')
  assert.equal(directiveRequest(draft({ target: 'intent', intentID: ' in_x ' }), 'k').intent_id, 'in_x')
})

test('the idempotency key follows content and recipient', () => {
  const first = directiveFingerprint(draft())
  assert.equal(first, directiveFingerprint(draft({ text: '  下一步先补 /api 的授权模型  ' })), 'whitespace-only edits are the same directive')
  assert.notEqual(first, directiveFingerprint(draft({ text: '换一句' })))
  assert.notEqual(first, directiveFingerprint(draft({ target: 'intent', intentID: 'in_x' })))

  const kept = directiveKeyFor({ fingerprint: first, key: 'stable-key' }, first, () => 'unused')
  assert.equal(kept.key, 'stable-key')
  let calls = 0
  const regenerated = directiveKeyFor({ fingerprint: first, key: 'stable-key' }, directiveFingerprint(draft({ text: '换一句' })), () => {
    calls++
    return 'new-key'
  })
  assert.equal(calls, 1)
  assert.equal(regenerated.key, 'new-key')
})

test('a message may only reach a direction that has not settled', () => {
  assert.deepEqual(
    oneShotDirections(directions).map((intent) => intent.id),
    ['in_running', 'in_pending'],
  )
  assert.equal(isDirectionFinished('done'), true)
  assert.equal(isDirectionFinished('parked'), true)
  assert.equal(isDirectionFinished('running'), false)
})

function result(over: Partial<DirectiveResponse>): DirectiveResponse {
  return {
    kind: 'message',
    target: 'commander',
    persistent: false,
    created: true,
    ...over,
  }
}

test('receipts say where the message went and never claim a boundary change', () => {
  const commander = directiveReceipt(result({ delivery: 'queued' }))
  assert.match(commander, /已发给指挥官/)
  assert.match(commander, /已排队/)
  assert.match(commander, /只影响它下一轮决策/)
  assert.doesNotMatch(commander, /持久指令|已记入授权链/)

  // A direction message that the server turned into an escalation says so.
  const escalated = directiveReceipt(
    result({ target: 'intent', intent_id: 'in_x', delivery: 'accepted', escalation: { id: 'esc_1' } }),
  )
  assert.match(escalated, /已送达该意图（in_x）/)
  assert.match(escalated, /宿主已接收/)
  assert.match(escalated, /并已上报指挥官（esc_1）/)

  // Without the server's escalation there is nothing to promise: the receipt
  // says the report is unconfirmed instead of inventing one.
  const notReported = directiveReceipt(result({ target: 'intent', intent_id: 'in_x', delivery: 'queued' }))
  assert.match(notReported, /上报回执未返回/)
  assert.doesNotMatch(notReported, /并已上报指挥官/)
  // No meta-talk about authorization anywhere: the operator asked for none.
  for (const line of [commander, escalated, notReported]) {
    assert.doesNotMatch(line, /授权|边界/)
  }
})

test('server error codes map to actionable words, not generic failures', () => {
  assert.match(
    directiveErrorMessage(new ApiError(409, 'finished', 'directive_target_finished')),
    /消息不能复活它/,
  )
  assert.match(
    directiveErrorMessage(new ApiError(404, 'missing', 'directive_target_missing')),
    /意图不存在/,
  )
  assert.match(
    directiveErrorMessage(new ApiError(429, 'full', 'message_backpressure')),
    /队列已满/,
  )
  // A 400 keeps the server's own reason.
  assert.equal(directiveErrorMessage(new ApiError(400, '收件人不存在', 'directive_invalid')), '收件人不存在')
  assert.equal(directiveErrorMessage(new Error('网络错误')), '网络错误')
})

test('the composer hint distinguishes running and idle projects', () => {
  assert.match(runningDirectionsHint(2), /在跑执行者 2 个/)
  assert.match(runningDirectionsHint(0), /没有在跑/)
})

test('api.directive posts the exact body and returns the delivery state', async () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const originalFetch = globalThis.fetch
  let sent: { url?: string; method?: string; body?: unknown } = {}
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: { pathname: '/p/fixture/' } },
  })
  globalThis.fetch = async (url, init) => {
    sent = {
      url: String(url),
      method: init?.method,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    }
    return new Response(
      JSON.stringify({
        kind: 'message',
        target: 'intent',
        intent_id: 'in_x',
        persistent: false,
        created: true,
        delivery: 'accepted',
        escalation: { id: 'esc_1' },
      }),
      { status: 202 },
    )
  }
  try {
    const body = directiveRequest(draft({ target: 'intent', intentID: 'in_x' }), 'key-9')
    const response = await api.directive(body)
    assert.equal(sent.url, '/p/fixture/api/directives')
    assert.equal(sent.method, 'POST')
    assert.deepEqual(sent.body, body)
    assert.equal(response.escalation?.id, 'esc_1')
  } finally {
    globalThis.fetch = originalFetch
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})
