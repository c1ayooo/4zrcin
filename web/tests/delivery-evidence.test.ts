// The 通信 page's aligned projection is rendered, not just computed: each
// ledger row carries its IOA observation (or the reason there is none), and one
// observed delivery shows the parsed header + the wire body. These are the two
// surfaces lib/alignment's tests cannot cover.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import { buildRecordMaps } from '../src/lib/ioa.ts'
import type { IOARecord, Message, Participant } from '../src/lib/types.ts'

const people: Participant[] = [
  { id: 'commander', kind: 'commander', title: '指挥官', contactable: true },
]

const ledgerMessage: Message = {
  id: 'm_1',
  seq: 1,
  project_id: 'fixture',
  from_participant_id: 'operator',
  to_participant_id: 'commander',
  kind: 'say',
  text: '控制台正文',
  status: 'accepted',
  delivery_path: 'ioa',
  delivery_id: 'ioa_1',
  created_at: '2026-11-19T06:00:00Z',
  updated_at: '2026-11-19T06:00:01Z',
}

const consoleRecord: IOARecord = {
  seq: 1,
  ioa_message_id: 'ioa_1',
  sender: 'cd3ecb7f657f17e27aef96ff92cf9312',
  observed_at: '2026-11-19T06:00:02Z',
  envelope: { event: 'message', source: {}, target: { session_id: 'sess_commander' } },
  content: {
    text: '[消息] from=operator（操作员/控制台令牌，不是指挥官） kind=say message_id=m_1\n控制台正文',
  },
}

const maps = buildRecordMaps({ sess_commander: '指挥官' }, people, [consoleRecord])

let server: Awaited<ReturnType<typeof createTestServer>>
let DeliveryEvidence: React.ComponentType<any>
let ObservedDelivery: React.ComponentType<any>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  const module = await server.ssrLoadModule('/src/components/DeliveryEvidence.tsx')
  DeliveryEvidence = module.DeliveryEvidence
  ObservedDelivery = module.ObservedDelivery
})

after(async () => {
  await server?.close()
})

test('a matched row announces the IOA delivery but keeps the body behind 展开', () => {
  const html = renderToStaticMarkup(
    React.createElement(DeliveryEvidence, { message: ledgerMessage, records: [consoleRecord], maps, fresh: new Set() }),
  )
  assert.match(html, /原始投递/)
  assert.match(html, /IOA/)
  assert.match(html, /展开/)
  assert.match(html, /aria-expanded="false"/)
  // Closed by default: the raw bytes are not rendered until the operator asks.
  assert.doesNotMatch(html, /控制台正文/)
})

test('a row without an observation states the ledger reason', () => {
  const html = renderToStaticMarkup(
    React.createElement(DeliveryEvidence, {
      message: { ...ledgerMessage, status: 'queued', delivery_path: undefined, delivery_id: undefined },
      records: [],
      maps,
      fresh: new Set(),
    }),
  )
  assert.match(html, /原始投递/)
  assert.match(html, /排队中，尚未投递/)
  assert.doesNotMatch(html, /展开/)
})

test('one observed delivery resolves the console header and shows only the body', () => {
  const html = renderToStaticMarkup(
    React.createElement(ObservedDelivery, { record: consoleRecord, maps, fresh: new Set() }),
  )
  // Parsed from [消息] from=operator — the old view printed 来源未标注 here.
  assert.match(html, /操作员/)
  assert.match(html, /控制台直投/)
  assert.match(html, /控制台正文/)
  assert.doesNotMatch(html, /\[消息\]/, 'the addressing header must not stay in the body')
  assert.match(html, /IOA/)
})

test('a re-queued delivery is visibly marked', () => {
  const retried: IOARecord = {
    ...consoleRecord,
    content: {
      text: '[重投] [消息] from=commander（本项目指挥官模型） kind=say message_id=m_1\n重投正文',
    },
  }
  const html = renderToStaticMarkup(
    React.createElement(ObservedDelivery, { record: retried, maps, fresh: new Set() }),
  )
  assert.match(html, /重投/)
  assert.match(html, /重投正文/)
})
