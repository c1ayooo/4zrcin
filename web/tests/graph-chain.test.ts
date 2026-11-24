// 渗透链路（graph/chain.ts）。
//
// 判据（Chief 定的验收）：侧栏跳表行数 = 图上高亮的主干边数 + 1，且每一跳都说
// 得清「上一跳怎么接过来的」。另外这条链一个节点喂给多个意图时，旁支必须留在
// 图上、并且在跳表里数得出来——砍成单线就是把 DAG 读成了链表。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildGraph } from '../src/graph/model.ts'
import { chainView } from '../src/graph/chain.ts'
import type { Fact, Goal, Intent } from '../src/lib/types.ts'

function fact(id: string, over: Partial<Fact> = {}): Fact {
  return {
    id,
    intent_id: 'in_1',
    kind: 'note',
    title: id,
    summary: '',
    confidence: 'observed',
    status: 'unverified',
    claim_version: 1,
    created_at: '2026-11-11T00:00:00Z',
    ...over,
  }
}

function intent(over: Partial<Intent> = {}): Intent {
  return {
    id: 'in_1',
    title: 'task',
    description: '',
    status: 'running',
    attempts: 1,
    created_at: '2026-11-11T00:00:00Z',
    updated_at: '2026-11-11T00:00:00Z',
    ...over,
  }
}

function goal(over: Partial<Goal> = {}): Goal {
  return { id: 'g_1', statement: '达成目标', met: false, created_at: '2026-11-11T00:00:00Z', ...over }
}

/** 简报 → in_1 → f_out → in_2 → f_later；f_out 还被 in_3 消费（旁支），
 *  in_3 又产出 f_branch，f_branch 再被 in_4 消费——二跳之外的东西不该进旁支。 */
function fixture() {
  return buildGraph(
    [
      fact('f_out', { intent_id: 'in_1', kind: 'vuln', title: '注入点' }),
      fact('f_later', { intent_id: 'in_2', kind: 'cred', title: '口令' }),
      fact('f_branch', { intent_id: 'in_3', kind: 'host', title: '旁支主机' }),
      fact('f_deep', { intent_id: 'in_4', kind: 'note', title: '更远一跳' }),
    ],
    [
      intent({ id: 'in_1', title: '扫描入口' }),
      intent({ id: 'in_2', title: '复用注入', from_fact_ids: ['f_out'], created_at: '2026-11-11T02:00:00Z' }),
      intent({ id: 'in_3', title: '另一条路', from_fact_ids: ['f_out'], created_at: '2026-11-11T03:00:00Z' }),
      intent({ id: 'in_4', title: '旁支再往下', from_fact_ids: ['f_branch'], created_at: '2026-11-11T04:00:00Z' }),
    ],
    [goal()],
  )
}

test('主干是一串有序跳：行数 = 主干边数 + 1', () => {
  const model = fixture()
  const chain = chainView(model, 'f:f_later')
  assert.ok(chain, '四跳的节点必须给出链路')
  assert.deepEqual(
    chain.hops.map((hop) => hop.id),
    ['p:project', 's:in_1', 'f:f_out', 's:in_2', 'f:f_later'],
  )
  assert.equal(chain.hops.length, chain.mainEdges.size + 1)
  assert.deepEqual(
    chain.hops.map((hop) => hop.index),
    [1, 2, 3, 4, 5],
  )
  // 侧栏行号与主干是同一份顺序：第 N 行就是 main 里的第 N 个节点。
  assert.deepEqual(
    chain.hops.map((hop) => hop.id),
    chain.main,
  )
})

test('每一跳都写明关系，而且用真正的边连到上一跳', () => {
  const model = fixture()
  const chain = chainView(model, 'f:f_later')
  assert.ok(chain)
  const byId = new Map(model.edges.map((e) => [e.id, e]))
  assert.equal(chain.hops[0].relation, null, '起点没有上一跳')
  assert.deepEqual(
    chain.hops.slice(1).map((hop) => hop.relation),
    ['premise', 'produce', 'premise', 'produce'],
  )
  for (let i = 1; i < chain.hops.length; i++) {
    const prev = chain.hops[i - 1].id
    const here = chain.hops[i].id
    assert.ok(chain.hops[i].edges.length > 0, `第 ${i + 1} 跳必须有边`)
    for (const edgeID of chain.hops[i].edges) {
      const edge = byId.get(edgeID)
      assert.ok(edge, `边 ${edgeID} 必须是模型里的真边`)
      assert.deepEqual(
        [edge.source, edge.target].sort(),
        [prev, here].sort(),
        `第 ${i + 1} 跳的边必须接住上一跳`,
      )
    }
  }
})

test('同一个事实喂给多个意图：旁支留在图上并数得出来', () => {
  const model = fixture()
  const chain = chainView(model, 'f:f_later')
  assert.ok(chain)
  const hop = chain.hops.find((h) => h.id === 'f:f_out')
  assert.ok(hop)
  assert.deepEqual(hop.otherUses.map((u) => u.id), ['s:in_3'])
  assert.equal(hop.otherUses[0].title, '另一条路')
  // 旁支意图与它的产物都在次亮层，主干节点不混进去。
  assert.ok(chain.near.has('s:in_3'), '旁支意图留在图上')
  assert.ok(chain.near.has('f:f_branch'), '旁支意图的产物也留在图上')
  for (const id of chain.main) assert.equal(chain.near.has(id), false, `主干节点 ${id} 不算旁支`)
  for (const edgeID of chain.mainEdges) {
    assert.equal(chain.nearEdges.has(edgeID), false, '主干边不进旁支层')
  }
})

test('旁支只到一跳：邻域含旁支意图与它的产物，不含二跳之外', () => {
  const model = fixture()
  const chain = chainView(model, 'f:f_later')
  assert.ok(chain)
  // 一跳：f_out 还被 in_3 消费，in_3 的产物 f_branch 也在旁支里。
  assert.equal(chain.near.has('s:in_3'), true, '旁支意图在邻域')
  assert.equal(chain.near.has('f:f_branch'), true, '旁支意图的产物在邻域')
  // 二跳之外：f_branch 的消费者 in_4 与它产出的 f_deep 不进邻域，也不进主干。
  assert.equal(chain.near.has('s:in_4'), false, '旁支再往下的意图不进旁支')
  assert.equal(chain.near.has('f:f_deep'), false, '二跳产物不进旁支')
  assert.equal(chain.main.includes('s:in_4'), false, '也不进主干')
  for (const id of chain.main) assert.equal(chain.near.has(id), false, `主干节点 ${id} 不算旁支`)
})

test('没有上游的节点只有简报这一跳，未知 id 不造链', () => {
  const model = buildGraph([], [intent({ id: 'in_solo' })], [])
  const chain = chainView(model, 's:in_solo')
  assert.ok(chain)
  assert.deepEqual(chain.hops.map((h) => h.id), ['p:project', 's:in_solo'])
  assert.equal(chain.hops[0].relation, null)
  assert.equal(chain.mainEdges.size, 1)
  assert.equal(chainView(model, 'f:missing'), null)
  assert.equal(chainView(model, null), null)
})
