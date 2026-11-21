// The time axis is an occasional tool, so it starts collapsed and only takes a
// strip of the map once the operator asks. The open state lasts for this page
// load (a view switch keeps it, a reload folds it back), which is what the
// module-level flag does — assert both halves so a future refactor cannot make
// it stick across reloads or fold back on every remount.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphTimeline: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GraphTimeline = (await server.ssrLoadModule('/src/components/graph/GraphTimeline.tsx')).GraphTimeline
})

afterEach(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
})

after(async () => {
  await server?.close()
})

const props = {
  total: 12,
  cursor: null,
  revealed: 12,
  pending: 0,
  untimed: 3,
  atLabel: '12:00:00',
  onCursor() {},
  playing: false,
  onTogglePlay() {},
}

function buttons(): any[] {
  return renderer!.root.findAll((node: any) => node.type === 'button')
}

function summary(): string {
  return JSON.stringify(renderer!.toJSON())
}

test('the axis opens collapsed: one line, no scrubber', async () => {
  await act(async () => {
    renderer = create(React.createElement(GraphTimeline, props))
  })
  assert.equal(renderer!.root.findAll((node: any) => node.type === 'input').length, 0, 'no slider until asked')
  const open = buttons().find((node: any) => String(node.props.title ?? '').startsWith('展开时间轴'))
  assert.ok(open, 'the collapsed strip needs a labelled way to open')
  assert.equal(open.props['aria-expanded'], false)
  // The summary still says how far the axis has revealed, so the closed strip
  // is not silent about the state it holds.
  assert.match(summary(), /已揭示/)
  assert.match(summary(), /无时间/, 'undated nodes stay counted while collapsed')
})

test('opening shows the scrubber, and the open state survives a remount in the same load', async () => {
  await act(async () => {
    renderer = create(React.createElement(GraphTimeline, props))
  })
  const open = buttons().find((node: any) => String(node.props.title ?? '').startsWith('展开时间轴'))
  await act(async () => {
    open.props.onClick()
  })
  assert.equal(renderer!.root.findAll((node: any) => node.type === 'input').length, 1, 'opening reveals the slider')

  // A view switch unmounts and remounts the map: the strip must stay open.
  await act(async () => {
    renderer!.unmount()
    renderer = create(React.createElement(GraphTimeline, props))
  })
  assert.equal(renderer!.root.findAll((node: any) => node.type === 'input').length, 1, 'a remount keeps it open')

  // Collapsing is remembered too, so the next mount in this load starts closed.
  const close = buttons().find((node: any) => node.props['aria-label'] === '收起时间轴')
  await act(async () => {
    close.props.onClick()
  })
  assert.equal(renderer!.root.findAll((node: any) => node.type === 'input').length, 0)
})
