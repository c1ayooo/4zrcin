import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  ASSET_FACT_KINDS,
  assetFenceOf,
  assetKindLabel,
  buildAssetGraphModel,
} from '../src/lib/asset-graph.ts'
import type { AssetGraph } from '../src/lib/types.ts'

function graph(over: Partial<AssetGraph>): AssetGraph {
  return { full: false, nodes: [], edges: [], totals: {}, unlinked_vulns: 0, ...over }
}

// The full view has no tree root: every row is a fact node at depth 0, and an
// empty fence reads 未登记 rather than "outside".
test('full view builds facts and normalizes the missing fence', () => {
  const model = buildAssetGraphModel(
    graph({
      full: true,
      nodes: [
        { id: 'asset:row:example.com', kind: 'domain', label: 'example.com', fence: 'in_fence' },
        { id: 'asset:row:10.0.0.0/8', kind: 'range', label: '10.0.0.0/8' },
        { id: 'asset:row:10.0.0.5', kind: 'host', label: '10.0.0.5', fence: '' },
      ],
    }),
  )
  assert.equal(model.nodes.length, 3)
  assert.ok(model.nodes.every((n) => n.type === 'fact'))
  assert.equal(model.byId.get('asset:row:example.com')?.fence, 'in_fence')
  assert.equal(model.byId.get('asset:row:10.0.0.5')?.fence, 'unregistered')
  assert.equal(model.byId.get('asset:row:10.0.0.0/8')?.fence, 'unregistered')
  assert.equal(model.maxDepth, 0)
  assert.equal(model.edges.length, 0)
})

// The drill-down root is the one project-shaped node the layered layout needs;
// every containment/hangs relation is a structural edge, and depth counts the
// hops from that root.
test('drill-down builds the root, the hierarchy and BFS depth', () => {
  const model = buildAssetGraphModel(
    graph({
      key: 'example.com',
      nodes: [
        { id: 'asset:row:example.com', kind: 'domain', label: 'example.com', fence: 'in_fence' },
        { id: 'asset:host:www.example.com', kind: 'host', label: 'www.example.com' },
        { id: 'asset:service:www.example.com:443', kind: 'service', label: 'www.example.com:443', host: 'www.example.com' },
        { id: 'asset:url:https://www.example.com/a', kind: 'url', label: '/a', url: 'https://www.example.com/a', host: 'www.example.com' },
        { id: 'asset:vuln:gv_1', kind: 'vuln', label: '越权', host: 'www.example.com' },
        { id: 'asset:intent:in_1', kind: 'intent', label: '打点', intent_id: 'in_1', status: 'done' },
        { id: 'asset:evidence:in_x/log.txt', kind: 'evidence', label: 'in_x/log.txt' },
      ],
      edges: [
        { id: 'e1', source: 'asset:row:example.com', target: 'asset:host:www.example.com', kind: 'contains' },
        { id: 'e2', source: 'asset:host:www.example.com', target: 'asset:service:www.example.com:443', kind: 'contains' },
        { id: 'e3', source: 'asset:service:www.example.com:443', target: 'asset:url:https://www.example.com/a', kind: 'contains' },
        { id: 'e4', source: 'asset:host:www.example.com', target: 'asset:vuln:gv_1', kind: 'hangs' },
        { id: 'e5', source: 'asset:url:https://www.example.com/a', target: 'asset:intent:in_1', kind: 'hangs' },
        { id: 'e6', source: 'asset:row:example.com', target: 'asset:evidence:in_x/log.txt', kind: 'hangs' },
      ],
    }),
  )
  const root = model.byId.get('asset:row:example.com')
  assert.equal(root?.type, 'project')
  assert.equal(root?.fence, 'in_fence')
  assert.equal(model.byId.get('asset:host:www.example.com')?.depth, 1)
  assert.equal(model.byId.get('asset:service:www.example.com:443')?.depth, 2)
  assert.equal(model.byId.get('asset:url:https://www.example.com/a')?.depth, 3)
  assert.equal(model.byId.get('asset:intent:in_1')?.depth, 4)
  assert.equal(model.byId.get('asset:vuln:gv_1')?.depth, 2)
  assert.equal(model.byId.get('asset:evidence:in_x/log.txt')?.depth, 1)
  assert.equal(model.maxDepth, 4)
  const intent = model.byId.get('asset:intent:in_1')
  assert.equal(intent?.kind, 'intent')
  assert.equal(intent?.status, 'done')
  assert.ok(model.edges.every((e) => e.kind === 'brief'))
  assert.deepEqual(model.incoming.get('asset:host:www.example.com'), ['asset:row:example.com'])
  assert.equal(model.byId.get('asset:host:www.example.com')?.degree, 3)
  for (const node of model.nodes) {
    assert.ok(Number.isFinite(node.x) && Number.isFinite(node.y))
  }
})

// An edge naming a node the response did not carry is dropped: the canvas must
// not draw a line into nothing.
test('dangling edges are skipped', () => {
  const model = buildAssetGraphModel(
    graph({
      key: 'example.com',
      nodes: [{ id: 'asset:row:example.com', kind: 'domain', label: 'example.com' }],
      edges: [{ id: 'e1', source: 'asset:row:example.com', target: 'asset:host:gone', kind: 'contains' }],
    }),
  )
  assert.equal(model.edges.length, 0)
  assert.equal(model.edgesById.size, 0)
})

test('asset kind vocabulary and missing-data fallback', () => {
  assert.ok(ASSET_FACT_KINDS.has('url'))
  assert.ok(ASSET_FACT_KINDS.has('domain'))
  assert.equal(assetKindLabel('domain'), '域名')
  assert.equal(assetKindLabel('range'), '网段')
  assert.equal(assetKindLabel('url'), 'URL')
  assert.equal(assetKindLabel('mystery'), '资产')
  assert.equal(assetFenceOf({ kind: 'vuln' }), undefined)
  assert.equal(assetFenceOf({ kind: 'domain' }), 'unregistered')
  assert.equal(assetFenceOf({ kind: 'domain', fence: 'pending' }), 'pending')
  const empty = buildAssetGraphModel(undefined)
  assert.equal(empty.nodes.length, 0)
  assert.equal(empty.maxDepth, 0)
})
