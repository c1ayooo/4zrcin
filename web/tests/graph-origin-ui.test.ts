// P0-9c UI: the provenance second channel and the helper badge are visible in
// the three surfaces the operator reads (图例 / 列表 / 节点面板). Loaded through
// the app's own Vite transform so JSX, aliases and @cyber/ui behave as in the
// console; there is no browser — the renderer is enough for static copy.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import type { GraphModel, GraphNode } from '../src/graph/model.ts'
import type { Intent } from '../src/lib/types.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let GraphLegend: React.ComponentType<any>
let GraphListView: React.ComponentType<any>
let NodePanel: React.ComponentType<any>
let api: any
let renderer: ReactTestRenderer | undefined
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GraphLegend = (await server.ssrLoadModule('/src/components/graph/GraphLegend.tsx')).GraphLegend
  GraphListView = (await server.ssrLoadModule('/src/components/graph/GraphListView.tsx')).GraphListView
  NodePanel = (await server.ssrLoadModule('/src/components/graph/GraphNodePanel.tsx')).GraphNodePanel
  api = (await server.ssrLoadModule('/src/lib/api.ts')).api
  api.fact = async () => null
  api.children = async () => []
  api.intentUsage = async () => null
  api.intentReadiness = async () => null
  api.premisePolicy = async () => ({ premise_policy: 'verified_only', effective: 'verified_only' })
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: { pathname: '/p/fixture/', search: '', hash: '' }, setInterval, clearInterval },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      visibilityState: 'visible',
      addEventListener() {},
      removeEventListener() {},
      getElementById() {
        return null
      },
    },
  })
})

afterEach(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
})

after(async () => {
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  await server?.close()
})

function mountedJSON(): string {
  return JSON.stringify(renderer!.toJSON())
}

const dock = {
  width: 360,
  min: 300,
  max: 760,
  resizing: false,
  onResizeStart() {},
  onResizeKey() {},
}

const panelProps = {
  facts: [],
  runs: [],
  dock,
  onSelectFact() {},
  onClose() {},
  onChanged() {},
  childRefresh: 0,
  onChildIntent() {},
}

function stepNode(over: Partial<GraphNode> = {}, intentOver: Partial<Intent> = {}): GraphNode {
  const intent: Intent = {
    id: 'in_peer',
    title: '平级意图',
    description: '',
    status: 'running',
    attempts: 1,
    created_at: '2026-11-11T00:00:00Z',
    updated_at: '2026-11-11T00:00:00Z',
    ...intentOver,
  }
  return {
    id: 's:in_peer',
    type: 'step',
    label: intent.title,
    status: intent.status,
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    degree: 0,
    w: 60,
    h: 20,
    depth: 2,
    attempts: 1,
    origin: 'executor',
    originIntent: { id: 'in_cmd', title: '指挥官意图' },
    lineageDepth: 1,
    helpers: { calls: 2, active: 1, durationMs: 1500 },
    meta: { intent },
    ...over,
  }
}

test('图例 explains the provenance shapes, the derived 发起线 and the temporary helper', async () => {
  await act(async () => {
    renderer = create(React.createElement(GraphLegend))
  })
  // The header is the entry to the collapsed legend.
  const open = renderer!.root.findAllByType('button')[0]
  assert.ok(open, 'the legend starts collapsed')
  await act(async () => {
    open!.props.onClick()
  })
  const html = mountedJSON()
  for (const copy of [
    '发起者（第二通道 · 形状）',
    '指挥官（实心菱形）',
    '执行者（空心圆）',
    '操作员（双环）',
    '未记录（不画）',
    '发起线（平级发布，派生）',
    '委托（等待）',
    '助手（原生 subagent，临时）',
    '状态色仍是第一通道',
  ]) {
    assert.ok(html.includes(copy), `legend is missing ${copy}`)
  }
})

test('列表视图 exposes 发起者 / 谱系深度 and never invents one', async () => {
  const nodes: GraphNode[] = [
    { ...stepNode(), label: '平级意图' },
    {
      ...stepNode({ id: 's:in_cmd', label: '指挥官意图', origin: 'commander', originIntent: undefined, lineageDepth: 0 }),
      meta: { intent: { ...stepNode().meta!.intent!, id: 'in_cmd', title: '指挥官意图', created_by_participant_id: 'commander' } },
    },
    {
      ...stepNode({
        id: 's:in_old',
        label: '旧意图',
        origin: 'unknown',
        originIntent: undefined,
        lineageDepth: undefined,
        helpers: undefined,
      }),
      meta: { intent: { ...stepNode().meta!.intent!, id: 'in_old', title: '旧意图', created_by_participant_id: undefined } },
    },
  ]
  const model = { nodes, edges: [] } as unknown as GraphModel
  await act(async () => {
    renderer = create(
      React.createElement(GraphListView, {
        model,
        selected: null,
        matches: null,
        onSelect() {},
        onClose() {},
      }),
    )
  })
  const html = mountedJSON()
  assert.ok(html.includes('发起者'), 'list header')
  assert.ok(html.includes('谱系'), 'lineage column')
  assert.ok(html.includes('执行者「指挥官意图」'))
  assert.ok(html.includes('指挥官'))
  // 谱系深度 0 renders as the number itself; undefined renders as "—".
  const zeroCell = renderer!.root.findAll((n) => n.type === 'span' && n.children.join('') === '0')
  assert.ok(zeroCell.length > 0, 'commander root lineage depth is shown as 0')
  assert.ok(html.includes('—'), 'unknown/legacy rows stay unclassified, not guessed')
})

test('选中步骤显示发起者解析、平级说明与本轮助手聚合', async () => {
  const node = stepNode({}, { created_by_participant_id: 'pt_in_cmd' })
  await act(async () => {
    renderer = create(React.createElement(NodePanel, { ...panelProps, node }))
  })
  const html = mountedJSON()
  for (const copy of [
    '执行者「指挥官意图」',
    '平级发布',
    '第 1 跳',
    '本轮助手',
    '调用 2 次',
    '1.5s',
    '不登记 run/usage',
    '不产生事实',
  ]) {
    assert.ok(html.includes(copy), `panel is missing ${copy}`)
  }
})

test('没有发起者记录、也没有助手调用时，面板不编造这两块', async () => {
  const node = stepNode({
    origin: undefined,
    originIntent: undefined,
    lineageDepth: undefined,
    helpers: undefined,
  })
  await act(async () => {
    renderer = create(React.createElement(NodePanel, { ...panelProps, node }))
  })
  const html = mountedJSON()
  assert.ok(html.includes('未记录'), 'legacy created_by reads 未记录')
  assert.equal(html.includes('本轮助手'), false, 'no helper block without a recorded call')
  assert.equal(html.includes('平级发布'), false, 'no peer note without an executor origin')
})
