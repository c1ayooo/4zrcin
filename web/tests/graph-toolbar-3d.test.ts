// 3D 星图的入口只出现在力导向下面：链路树是读链视图，三维在追链上必输，所以不
// 给它一个会误导的入口。开着 3D 时，2D 专属的缩放/适配/重排先收起来——它们作用
// 于被盖住的 2D 画布，点了看不到变化。
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphToolbar: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GraphToolbar = (await server.ssrLoadModule('/src/components/graph/GraphToolbar.tsx')).GraphToolbar
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

const base = {
  chatDock: false,
  onToggleChatDock() {},
  layout: 'force',
  onLayout() {},
  showLabels: true,
  onToggleLabels() {},
  listView: false,
  onToggleListView() {},
  onZoom() {},
  onFit() {},
  onRelayout() {},
  timelineOpen: false,
  onToggleTimeline() {},
  overview3d: false,
  onToggle3D() {},
}

function props(overrides: Record<string, unknown> = {}) {
  return { ...base, ...overrides }
}

function buttons(): any[] {
  return renderer!.root.findAll((node: any) => node.type === 'button')
}

function named(label: string): any {
  return buttons().find((node: any) => node.props['aria-label'] === label)
}

test('the 3D switch exists on the force layout only', async () => {
  await act(async () => {
    renderer = create(React.createElement(GraphToolbar, props()))
  })
  assert.ok(named('3D 星图'), '力导向下应有 3D 星图开关')
  assert.equal(named('3D 星图').props['aria-pressed'], false, '默认关')
  assert.ok(named('放大'), '没开 3D 时 2D 缩放仍在')

  await act(async () => {
    renderer!.update(React.createElement(GraphToolbar, props({ layout: 'layered' })))
  })
  assert.equal(named('3D 星图'), undefined, '链路树下不给 3D 入口')
})

test('pressing 3D reports intent and hides the 2D-only controls while open', async () => {
  let toggles = 0
  await act(async () => {
    renderer = create(
      React.createElement(GraphToolbar, props({ onToggle3D: () => { toggles += 1 } })),
    )
  })
  await act(async () => {
    named('3D 星图').props.onClick()
  })
  assert.equal(toggles, 1, '点开关要把意图交回页面')

  await act(async () => {
    renderer!.update(React.createElement(GraphToolbar, props({ overview3d: true })))
  })
  assert.ok(named('3D 星图'), '开着时开关仍在，才能关回来')
  assert.equal(named('3D 星图').props['aria-pressed'], true, '开着时状态可读')
  for (const label of ['放大', '缩小', '适配画布', '重新布局']) {
    assert.equal(named(label), undefined, `${label} 在 3D 星图下应收起`)
  }
})
