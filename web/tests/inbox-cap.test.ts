// The overview's first screen has to keep 目标进度 and 阻塞原因 in view, so the
// operator inbox lists three rows and hides the rest behind 展开全部. Hiding a
// row must never drop its record link: the row itself is the link, so it comes
// back unchanged when the operator asks for the rest.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import type { OperatorInbox } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let OperatorInboxCard: React.ComponentType<any>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  OperatorInboxCard = (await server.ssrLoadModule('/src/components/OperatorInboxCard.tsx')).OperatorInboxCard
})

after(async () => {
  await server?.close()
})

const at = '2026-11-21T04:00:00Z'

/** One row in every category, so a cap of three has to cut across groups. */
function fiveRows(): OperatorInbox {
  return {
    authorization: [
      { id: 'req_1', action: '需要授权登录门户', status: 'pending', created_at: at, updated_at: at },
    ],
    handoff: [
      { intent_id: 'in_1', title: '意图一', status: 'pending', request: { id: 'rq_1', request: '请在浏览器完成登录' } },
      { intent_id: 'in_2', title: '意图二', status: 'pending', request: { id: 'rq_2', request: '请确认验证码已经输入' } },
    ],
    acceptance: [{ goal_id: 'g_1', statement: '目标完成依据已变化' }],
    warnings: [{ run: 'r_1', warnings: ['交接缺少验收条目'] }],
    total: 5,
  }
}

const noop = () => {}

function render(inbox: OperatorInbox): string {
  return renderToStaticMarkup(
    React.createElement(OperatorInboxCard, {
      inbox,
      onNavigate: noop,
      onChanged: noop,
      onOpenIntent: noop,
      onOpenRun: noop,
      onOpenAuthorization: noop,
      onOpenGoal: noop,
    }),
  )
}

test('five rows render three, and the footer says how many are left', () => {
  const html = render(fiveRows())
  // The first three rows keep their own record links.
  assert.match(html, /需要授权登录门户/)
  assert.match(html, /请在浏览器完成登录/)
  assert.match(html, /请确认验证码已经输入/)
  assert.match(html, /在授权页打开请求 req_1/)
  // The fourth and fifth are behind the expander, not silently dropped.
  assert.doesNotMatch(html, /目标完成依据已变化/)
  assert.doesNotMatch(html, /r_1 有交接告警/)
  assert.match(html, /还有/)
  assert.match(html, /2/)
  assert.match(html, /展开全部/)
})

test('three rows fit whole and show no expander', () => {
  const inbox = fiveRows()
  inbox.handoff = inbox.handoff.slice(0, 2)
  inbox.acceptance = []
  inbox.warnings = []
  inbox.total = 3
  const html = render(inbox)
  assert.match(html, /请确认验证码已经输入/)
  assert.doesNotMatch(html, /展开全部/)
  assert.doesNotMatch(html, /收起/)
})

test('the expander counts rows, not the inbox total', () => {
  // A total that counts categories the card cannot list (a tool approval is a
  // state line, not a row) must not make the footer say there is more to show.
  const inbox = fiveRows()
  inbox.total = 7
  const html = render(inbox)
  assert.match(html, /还有/)
  assert.match(html, /2/)
})
