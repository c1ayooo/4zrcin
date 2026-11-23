// P0-9c 阶段2/3：图谱「发起者样式」与「原生助手层」。
//
// The graph has two channels: status colour (unchanged) and provenance shape.
// This file pins the derived semantics — the pure model and the render shapes —
// without a DOM:
//   - created_by classification (commander / operator / executor / unknown)
//   - the derived 发起边 (executor peer launches) exists, is not a degree input
//     and changes neither structural depth nor either layout
//   - native-subagent aggregates come from the backend Run fields, never from a
//     journal decode, and never invent a helper when no call was recorded
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  buildGraph,
  edgeKindLabel,
  ghostID,
  intentOrigin,
  originLabel,
  stepID,
} from '../src/graph/model.ts'
import type { GraphNode } from '../src/graph/model.ts'
import { originMarker } from '../src/graph/render.ts'
import { ForceSim } from '../src/graph/sim.ts'
import type { Intent, Run } from '../src/lib/types.ts'

function intent(over: Partial<Intent> = {}): Intent {
  return {
    id: 'in_task',
    title: 'task',
    description: '',
    status: 'running',
    attempts: 1,
    created_at: '2026-11-11T00:00:00Z',
    updated_at: '2026-11-11T00:00:00Z',
    ...over,
  }
}

function run(over: Partial<Run> = {}): Run {
  return {
    id: 'r_task',
    intent_id: 'in_task',
    node: 'local',
    status: 'done',
    started_at: '2026-11-11T00:00:00Z',
    events: 0,
    declared_facts: 0,
    ...over,
  }
}

/** The parts a layout comparison must not move. */
function laidOut(model: ReturnType<typeof buildGraph>): string {
  return JSON.stringify(model.nodes.map((n) => [n.id, n.depth, n.degree]))
}

// ---- 发起者分类 -----------------------------------------------------------

test('intentOrigin reads only created_by and never guesses a fourth role', () => {
  assert.equal(intentOrigin(intent({ created_by_participant_id: 'commander' })), 'commander')
  assert.equal(intentOrigin(intent({ created_by_participant_id: 'operator' })), 'operator')
  assert.equal(intentOrigin(intent({ created_by_participant_id: 'pt_in_abc123' })), 'executor')
  // Legacy/absent rows and the bare prefix are unknown, not an invented role.
  assert.equal(intentOrigin(intent({})), 'unknown')
  assert.equal(intentOrigin(intent({ created_by_participant_id: '' })), 'unknown')
  assert.equal(intentOrigin(intent({ created_by_participant_id: '   ' })), 'unknown')
  assert.equal(intentOrigin(intent({ created_by_participant_id: 'pt_' })), 'unknown')
  assert.equal(intentOrigin(intent({ created_by_participant_id: 'someone' })), 'unknown')
  // Copy is part of the same contract as edgeKindLabel.
  assert.equal(originLabel('commander'), '指挥官')
  assert.equal(originLabel('operator'), '操作员')
  assert.equal(originLabel('executor'), '执行者')
  assert.equal(originLabel('unknown'), '未记录')
  assert.equal(originLabel(undefined), '未记录')
})

test('the second channel is shape, and unknown/legacy draws nothing', () => {
  assert.equal(originMarker('commander'), 'diamond')
  assert.equal(originMarker('executor'), 'circle')
  assert.equal(originMarker('operator'), 'double-ring')
  assert.equal(originMarker('unknown'), null)
  assert.equal(originMarker(undefined), null)
})

test('delegation copy says 委托（等待） and the derived origin line is named 发起', () => {
  assert.equal(edgeKindLabel('spawn'), '委托（等待）')
  assert.equal(edgeKindLabel('origin'), '发起')
})

// ---- 发起边（派生，非账本） -------------------------------------------------

function originEdges(model: ReturnType<typeof buildGraph>) {
  return model.edges.filter((e) => e.kind === 'origin')
}

test('an executor-launched parentless direction points back at its initiator', () => {
  const graph = buildGraph(
    [],
    [
      intent({ id: 'in_cmd', title: '指挥官意图', created_by_participant_id: 'commander', status: 'done' }),
      intent({ id: 'in_peer', title: '平级意图', created_by_participant_id: 'pt_in_cmd' }),
    ],
    [],
  )
  const edges = originEdges(graph)
  assert.equal(edges.length, 1)
  assert.equal(edges[0].source, stepID('in_cmd'))
  assert.equal(edges[0].target, stepID('in_peer'))
  assert.equal(edges[0].intent?.id, 'in_peer', 'the edge opens the launched direction')
  assert.equal(edges[0].parentIntent, 'in_cmd')
  assert.equal(edges[0].label, '发起')
  // Provenance marks and origin lineage depth ride on step AND ghost.
  assert.equal(graph.byId.get(stepID('in_cmd'))?.origin, 'commander')
  assert.equal(graph.byId.get(stepID('in_cmd'))?.lineageDepth, 0)
  assert.equal(graph.byId.get(stepID('in_peer'))?.origin, 'executor')
  assert.equal(graph.byId.get(stepID('in_peer'))?.originIntent?.title, '指挥官意图')
  assert.equal(graph.byId.get(stepID('in_peer'))?.lineageDepth, 1)
  assert.equal(graph.byId.get(ghostID('in_peer'))?.origin, 'executor')
  assert.equal(graph.byId.get(ghostID('in_peer'))?.lineageDepth, 1)
})

test('the origin line is excluded from degree, depth and both layouts', () => {
  const intents = [
    intent({ id: 'in_cmd', title: '指挥官意图', created_by_participant_id: 'commander' }),
    intent({ id: 'in_peer', title: '平级意图', created_by_participant_id: 'pt_in_cmd' }),
    intent({
      id: 'in_peer2',
      title: '二级平级',
      created_by_participant_id: 'pt_in_peer',
      // No premise: a parentless direction starts from the brief.
    }),
  ]
  const withOrigin = buildGraph([], intents, [])
  // Same directions without the provenance fields: no origin edge may exist.
  const baseline = buildGraph(
    [],
    intents.map((it) => ({ ...it, created_by_participant_id: undefined })),
    [],
  )
  assert.equal(originEdges(baseline).length, 0)
  assert.equal(originEdges(withOrigin).length, 2)
  // Lineage is a separate scale from structural depth.
  assert.equal(withOrigin.byId.get(stepID('in_peer2'))?.lineageDepth, 2)
  // degree counts every edge except the derived origin line.
  for (const n of withOrigin.nodes) {
    const incident = withOrigin.edges.filter(
      (e) => e.kind !== 'origin' && (e.source === n.id || e.target === n.id),
    ).length
    assert.equal(n.degree, incident, `${n.id} degree must ignore the origin line`)
  }
  // Structural depth is identical with and without the derived edge.
  assert.equal(laidOut(withOrigin), laidOut(baseline))
  // Neither layout moves when the derived edge exists: the layered tree ignores
  // it and the force tick skips its spring.
  const layeredA = new ForceSim(withOrigin, { mode: 'layered' })
  const layeredB = new ForceSim(baseline, { mode: 'layered' })
  const posOf = (sim: ForceSim) => sim.nodes.map((n) => [n.id, n.x, n.y])
  assert.deepEqual(posOf(layeredA), posOf(layeredB))
  const forceA = new ForceSim(withOrigin, { mode: 'force' })
  const forceB = new ForceSim(baseline, { mode: 'force' })
  forceA.tick()
  forceB.tick()
  assert.deepEqual(posOf(forceA), posOf(forceB))
  // But the drawn/hit-tested edge list still carries it.
  assert.equal(forceA.edges.filter((e) => e.kind === 'origin').length, 2)
})

test('delegated children and commander/operator roots do not get origin lines', () => {
  const graph = buildGraph(
    [],
    [
      intent({ id: 'in_cmd', created_by_participant_id: 'commander' }),
      intent({ id: 'in_oper', created_by_participant_id: 'operator' }),
      intent({ id: 'in_child', parent_intent_id: 'in_cmd', created_by_participant_id: 'pt_in_cmd' }),
    ],
    [],
  )
  assert.equal(originEdges(graph).length, 0, 'a delegated child hangs off its parent, not an origin line')
  assert.equal(graph.byId.get(stepID('in_cmd'))?.lineageDepth, 0)
  assert.equal(graph.byId.get(stepID('in_oper'))?.lineageDepth, 0)
  assert.equal(graph.byId.get(stepID('in_child'))?.lineageDepth, undefined)
})

test('an unresolvable initiator is skipped; delegated_by_run_id is the fallback', () => {
  const intents = [
    intent({ id: 'in_cmd', title: '指挥官意图', created_by_participant_id: 'commander' }),
    intent({
      id: 'in_lost',
      created_by_participant_id: 'pt_in_not_loaded',
      delegated_by_run_id: 'r_missing',
    }),
    intent({
      id: 'in_via_run',
      created_by_participant_id: 'pt_in_not_loaded',
      delegated_by_run_id: 'r_cmd',
    }),
  ]
  const runs = [run({ id: 'r_cmd', intent_id: 'in_cmd', purpose: '' })]
  const graph = buildGraph([], intents, [], undefined, runs)
  const edges = originEdges(graph)
  assert.equal(edges.length, 1, 'only the resolvable creator line is drawn')
  assert.equal(edges[0].target, stepID('in_via_run'))
  assert.equal(edges[0].source, stepID('in_cmd'))
  assert.equal(graph.byId.get(stepID('in_lost'))?.origin, 'executor', 'the mark still says executor')
  assert.equal(graph.byId.get(stepID('in_lost'))?.originIntent, undefined)
  assert.equal(graph.byId.get(stepID('in_lost'))?.lineageDepth, undefined)
})

// ---- 原生助手聚合（后端字段，无 journal 解码） -----------------------------

test('helper aggregates come from the latest executor run that actually called one', () => {
  const runs = [
    run({
      id: 'r_old',
      intent_id: 'in_task',
      started_at: '2026-11-11T00:00:00Z',
      native_subagent_calls: 2,
      native_subagent_active: 0,
      native_subagent_duration_ms: 1500,
    }),
    // A newer executor run with no helper does not erase the last recorded turn.
    run({ id: 'r_new_empty', intent_id: 'in_task', started_at: '2026-11-12T00:00:00Z' }),
    // A support run's helper count is not this direction's turn.
    run({
      id: 'r_verifier',
      intent_id: 'in_task',
      purpose: 'verifier',
      started_at: '2026-11-13T00:00:00Z',
      native_subagent_calls: 9,
      native_subagent_active: 9,
      native_subagent_duration_ms: 9000,
    }),
  ]
  const graph = buildGraph([], [intent()], [], undefined, runs)
  const step = graph.byId.get(stepID('in_task'))!
  assert.deepEqual(step.helpers, { calls: 2, active: 0, durationMs: 1500 })
  assert.deepEqual(graph.byId.get(ghostID('in_task'))!.helpers, step.helpers)

  // A newer executor run with calls wins (active/duration come from that run).
  const live = buildGraph(
    [],
    [intent()],
    [],
    undefined,
    [
      ...runs,
      run({
        id: 'r_live',
        intent_id: 'in_task',
        started_at: '2026-11-14T00:00:00Z',
        native_subagent_calls: 3,
        native_subagent_active: 1,
        native_subagent_duration_ms: 400,
      }),
    ],
  )
  assert.deepEqual(live.byId.get(stepID('in_task'))!.helpers, { calls: 3, active: 1, durationMs: 400 })
})

test('no helper record means no field at all — nothing is invented', () => {
  const graph = buildGraph(
    [],
    [intent()],
    [],
    undefined,
    [
      run({ id: 'r_plain', intent_id: 'in_task' }),
      run({ id: 'r_verifier', intent_id: 'in_task', purpose: 'verifier', native_subagent_calls: 4 }),
    ],
  )
  for (const node of graph.nodes as GraphNode[]) {
    assert.equal(node.helpers, undefined, `${node.id} must not carry an empty helper block`)
  }
  assert.equal(JSON.stringify(graph.nodes).includes('helpers'), false)
})
