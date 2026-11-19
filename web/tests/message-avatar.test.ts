// The 通信 page used to print the sender name's first character in the avatar
// circle. For the console's own 「操作员」 that glyph is 「操」, and for a
// direction it is an arbitrary character of a long title. These tests pin the
// replacement: the role resolves from identity (id / kind), and the avatar
// renders a role icon instead of text.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'
import { participantRole } from '../src/lib/ioa.ts'
import type { Participant } from '../src/lib/types.ts'

const people: Participant[] = [
  { id: 'commander', kind: 'commander', title: '指挥官', contactable: true },
  { id: 'pt_in_cao', kind: 'intent', intent_id: 'in_cao', title: '操作顺序核查：合成目标', status: 'running', contactable: true },
]

test('the role comes from the participant id and kind, never from the title', () => {
  assert.equal(participantRole('operator', people), 'operator')
  assert.equal(participantRole('commander', people), 'commander')
  // A direction titled 「操作…」 is still an executor: its first glyph must not
  // decide the avatar.
  assert.equal(participantRole('pt_in_cao', people), 'executor')
  // An id missing from the participant list still gets a role, not a title.
  assert.equal(participantRole('pt_missing', people), 'executor')
})

let server: Awaited<ReturnType<typeof createTestServer>>
let SenderAvatar: React.ComponentType<{ role: string; className?: string }>

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  SenderAvatar = (await server.ssrLoadModule('/src/components/SenderAvatar.tsx')).SenderAvatar
})

after(async () => {
  await server?.close()
})

test('the avatar renders a role icon and no sender glyph', () => {
  const operator = renderToStaticMarkup(React.createElement(SenderAvatar, { role: 'operator' }))
  const commander = renderToStaticMarkup(React.createElement(SenderAvatar, { role: 'commander' }))
  const executor = renderToStaticMarkup(React.createElement(SenderAvatar, { role: 'executor' }))

  for (const html of [operator, commander, executor]) {
    assert.match(html, /<svg/, 'the avatar must be an icon')
    assert.doesNotMatch(html, /操|指/, 'the avatar must not print a name glyph')
    assert.doesNotMatch(html, />[^<]+</, 'the avatar must not render text')
  }
  assert.notEqual(operator, commander, 'operator and commander need distinct marks')
  assert.notEqual(commander, executor, 'commander and executor need distinct marks')
  assert.match(operator, /text-primary/, 'the operator keeps the accent colour')
  assert.match(commander, /text-ai/, 'the commander keeps the ai hue')
})
