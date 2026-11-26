// The approvals page is the one place an operator looks when something waits:
// both queues (tool/command reviews and fence-out assets) render on one screen.
// The page itself is thin wiring over the two existing cards, so the smoke test
// pins the shell — both sections present, the page reachable under its own view
// id — while the cards keep their own suites.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let Approvals: React.ComponentType
let ConfirmProvider: React.ComponentType

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  const module = await server.ssrLoadModule('/src/views/Approvals.tsx')
  Approvals = module.Approvals
  const ui = await server.ssrLoadModule('/packages/ui/src/confirm-dialog.tsx')
  ConfirmProvider = ui.ConfirmProvider
})

after(async () => {
  await server?.close()
})

test('the approvals page carries both queues on one screen', () => {
  // useConfirm needs the app's provider, exactly as the shell mounts it.
  const html = renderToStaticMarkup(
    React.createElement(ConfirmProvider, null, React.createElement(Approvals)),
  )
  assert.match(html, /审批/)
  assert.match(html, /工具与命令/)
  assert.match(html, /资产待审/)
  // The operator-facing contract stays on the page: an approval releases the
  // intercepted call or admits the asset — it is not an authorization edit.
  assert.match(html, /不改变授权边界/)
})

test('the project nav reaches the page under ?view=approvals', async () => {
  // The NAV array is the router's own source of view ids; a removed entry
  // would silently 404 the deep link and the rail button together.
  const { readFile } = await import('node:fs/promises')
  const source = await readFile(fileURLToPath(new URL('../src/views/GraphView.tsx', import.meta.url)), 'utf8')
  assert.match(source, /\{ id: 'approvals', label: '审批', icon: ClipboardCheck \}/)
  assert.match(source, /'approvals', 'access'/)
  // VIEW_ALIASES is the single list of accepted view ids: without the entry,
  // the rail item renders but the click resolves nowhere — the reported
  // "无法点击".
  assert.match(source, /approvals: \{ view: 'approvals' \}/)
})
