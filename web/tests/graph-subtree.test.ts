// 「只看这棵树」：`graph/chain.ts` 的 `subtree` + `onlyIds` 过滤。
//
// 这条动作的用途是「顺着起点读过来，别被无关节点打扰」，所以它必须是**树**：
// 全部祖先（DAG 里同一步的每个前提、每条分支都算）加自己，再带向下两跳产物；
// 不是 mainPath 那根单线。过滤只在这个动作开着时生效，未设/空集合与现在一模一样。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildGraph } from '../src/graph/model.ts'
import { subtree } from '../src/graph/chain.ts'
import { resolveGraphModelMemo, type GraphModelSource } from '../src/lib/graph-model-cache.ts'
import { defaultGraphFilters } from '../src/lib/graph-model-cache.ts'
import type { GraphRead } from '../src/lib/api.ts'
import type { Fact, Goal, Intent } from '../src/lib/types.ts'

const NOW = Date.parse('2026-11-24T08:00:00Z')

function fact(id: string, over: Partial<Fact> = {}): Fact {
  return {
    id,
    intent_id: 'in_scan',
    kind: 'note',
    title: id,
    summary: '',
    confidence: 'observed',
    status: 'verified',
    claim_version: 1,
    created_at: '2026-11-24T00:00:00Z',
    ...over,
  }
}

function intent(over: Partial<Intent> = {}): Intent {
  return {
    id: 'in_scan',
    title: 'task',
    description: '',
    status: 'done',
    attempts: 1,
    created_at: '2026-11-24T00:00:00Z',
    updated_at: '2026-11-24T00:00:00Z',
    ...over,
  }
}

/**
 * 分叉的 DAG：
 *   简报 → in_scan ─┬→ f_a ─┐
 *                   └→ f_b ─┴→ in_join ─┬→ f_join_a → in_deep1 → f_deep_1
 *                                       └→ f_join_b
 * 点 in_join：祖先里两条分支（f_a / f_b）都要在；兄弟产物 f_join_b 也在；
 * 向下只到两跳（f_deep_1 是第三跳，不在）。
 */
function fixture() {
  return buildGraph(
    [
      fact('f_a', { intent_id: 'in_scan', kind: 'host' }),
      fact('f_b', { intent_id: 'in_scan', kind: 'host' }),
      fact('f_join_a', { intent_id: 'in_join', kind: 'cred' }),
      fact('f_join_b', { intent_id: 'in_join', kind: 'cred' }),
      fact('f_deep_1', { intent_id: 'in_deep1', kind: 'loot' }),
    ],
    [
      intent({ id: 'in_scan', title: '扫描' }),
      intent({ id: 'in_join', title: '合并两条分支', from_fact_ids: ['f_a', 'f_b'], created_at: '2026-11-24T01:00:00Z' }),
      intent({ id: 'in_deep1', title: '往下走', from_fact_ids: ['f_join_a'], created_at: '2026-11-24T02:00:00Z' }),
    ],
    [] as Goal[],
  )
}

function sourceOf(model: ReturnType<typeof fixture>): GraphModelSource {
  const data: GraphRead = {
    facts: [...model.factsById.values()],
    intents: model.nodes
      .filter((n) => n.type === 'step' && n.meta?.intent)
      .map((n) => n.meta!.intent!),
    goals: [],
    edges: model.edges.map((e) => ({ source: e.source, target: e.target, kind: e.kind })),
    boundary: [],
    has_more: false,
    total: model.nodes.length,
    snapshot_version: { epoch: 'e_tree', seq: 1 },
  }
  return { data, runs: [], defaultModel: 'model-a' }
}

test('祖先走全部分支，不是主干单线', () => {
  const model = fixture()
  const ids = subtree(model, 's:in_join')
  assert.ok(ids)
  for (const id of ['s:in_join', 'f:f_a', 'f:f_b', 's:in_scan', 'p:project']) {
    assert.equal(ids.has(id), true, `${id} 必须在树里`)
  }
  // 同一步的另一个产物（兄弟）也在这棵树里，否则读不出这一步产出了什么。
  assert.equal(ids.has('f:f_join_b'), true, '兄弟产物在树里')
})

test('向下只带两跳：第三跳不在树里', () => {
  const model = fixture()
  const ids = subtree(model, 's:in_join')
  assert.ok(ids)
  assert.equal(ids.has('f:f_join_a'), true, '第一跳产物')
  assert.equal(ids.has('s:in_deep1'), true, '第二跳（产物的消费者）')
  assert.equal(ids.has('f:f_deep_1'), false, '第三跳不在')
})

test('过滤后模型只剩这棵树：节点数等于集合大小，边两端都在集合里', () => {
  const model = fixture()
  const ids = subtree(model, 's:in_join')
  assert.ok(ids)
  const filters = { ...defaultGraphFilters(), onlyIds: ids }
  const built = resolveGraphModelMemo(null, sourceOf(model), filters, NOW).model
  assert.equal(built.nodes.length, ids.size, `过滤后的节点数应等于树的大小，实际 ${built.nodes.length} / ${ids.size}`)
  for (const edge of built.edges) {
    assert.equal(ids.has(edge.source) && ids.has(edge.target), true, `边 ${edge.source}→${edge.target} 不该把树外的节点带回来`)
  }
  assert.equal(built.nodes.some((n) => n.id === 'f:f_deep_1'), false, '树外节点不绘制')
})

test('不设或空集合时与现在逐节点相同', () => {
  const model = fixture()
  const src = sourceOf(model)
  const plain = resolveGraphModelMemo(null, src, defaultGraphFilters(), NOW)
  // 同一次轮询再算一遍：缓存必须原样复用。
  assert.equal(resolveGraphModelMemo(plain, src, defaultGraphFilters(), NOW), plain)
  const empty = resolveGraphModelMemo(null, src, { ...defaultGraphFilters(), onlyIds: new Set() }, NOW)
  assert.deepEqual(
    empty.model.nodes.map((n) => n.id),
    plain.model.nodes.map((n) => n.id),
    '空集合按「没设」处理，一个节点都不能少',
  )
})

test('未知 id 与空环：不造树，环按它的意图算', () => {
  const model = fixture()
  assert.equal(subtree(model, null), null)
  assert.equal(subtree(model, 'f:missing'), null)
  // 没有产出的事实在模型里是空环（w:），点它等价于点那一步；环本身也留在这棵树里
  // （它就是同一个意图的另一种画法，数节点时要和屏幕一致）。
  const bare = buildGraph([], [intent({ id: 'in_bare' })], [])
  const ring = bare.nodes.find((n) => n.type === 'ghost')
  assert.ok(ring, '空意图应当有一个空环')
  const ids = subtree(bare, ring.id)
  assert.ok(ids)
  assert.equal(ids.has('s:in_bare'), true, '空环换成它的意图本身')
  assert.equal(ids.has(ring.id), true, '环与它的意图是同一跳的两种画法')
  assert.equal(ids.has('p:project'), true, '简报仍是这棵树的根')
})
