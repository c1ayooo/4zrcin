import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  ASSET_FACT_KINDS,
  assetFenceOf,
  assetKindLabel,
  buildAssetGraphModel,
  filterAssetModel,
} from '../src/lib/asset-graph.ts'
import type { AssetGraph } from '../src/lib/types.ts'

function graph(over: Partial<AssetGraph>): AssetGraph {
  return { root: 'asset:project', nodes: [], edges: [], totals: {}, unlinked_vulns: 0, ...over }
}

// The tree is rooted at the project card: the one pill the layered layout
// anchors on, no fence of its own, and an empty fence reads 未登记 rather
// than "outside".
test('the root card and the fence normalization', () => {
  const model = buildAssetGraphModel(
    graph({
      nodes: [
        { id: 'asset:project', kind: 'project', label: '验收项目' },
        { id: 'asset:row:example.com', kind: 'domain', label: 'example.com', fence: 'in_fence' },
        { id: 'asset:row:10.0.0.0/8', kind: 'range', label: '10.0.0.0/8' },
        { id: 'asset:host:www.example.com', kind: 'host', label: 'www.example.com', fence: '' },
        { id: 'asset:service:www.example.com:443', kind: 'service', label: ':443 nginx', host: 'www.example.com' },
        { id: 'asset:url:https://www.example.com/a', kind: 'url', label: '/a', url: 'https://www.example.com/a' },
      ],
      edges: [
        { id: 'e1', source: 'asset:project', target: 'asset:row:example.com', kind: 'contains' },
        { id: 'e2', source: 'asset:project', target: 'asset:row:10.0.0.0/8', kind: 'contains' },
        { id: 'e3', source: 'asset:row:example.com', target: 'asset:host:www.example.com', kind: 'contains' },
        { id: 'e4', source: 'asset:host:www.example.com', target: 'asset:service:www.example.com:443', kind: 'contains' },
        { id: 'e5', source: 'asset:service:www.example.com:443', target: 'asset:url:https://www.example.com/a', kind: 'contains' },
      ],
    }),
  )
  const root = model.byId.get('asset:project')
  assert.equal(root?.type, 'project')
  assert.equal(root?.fence, undefined)
  assert.equal(model.byId.get('asset:row:example.com')?.fence, 'in_fence')
  assert.equal(model.byId.get('asset:row:10.0.0.0/8')?.fence, 'unregistered')
  assert.equal(model.byId.get('asset:host:www.example.com')?.fence, 'unregistered')
  // The layered columns: card 0 → row 1 → host 2 → interface 3 → URL 4.
  assert.equal(model.byId.get('asset:row:example.com')?.depth, 1)
  assert.equal(model.byId.get('asset:host:www.example.com')?.depth, 2)
  assert.equal(model.byId.get('asset:service:www.example.com:443')?.depth, 3)
  assert.equal(model.byId.get('asset:url:https://www.example.com/a')?.depth, 4)
  assert.equal(model.maxDepth, 4)
  assert.ok(model.edges.every((e) => e.kind === 'brief'))
  assert.equal(model.byId.get('asset:host:www.example.com')?.degree, 2)
})

// An edge naming a node the response did not carry is dropped: the canvas must
// not draw a line into nothing.
test('dangling edges are skipped', () => {
  const model = buildAssetGraphModel(
    graph({
      nodes: [{ id: 'asset:project', kind: 'project', label: '项目' }],
      edges: [{ id: 'e1', source: 'asset:project', target: 'asset:host:gone', kind: 'contains' }],
    }),
  )
  assert.equal(model.edges.length, 0)
  assert.equal(model.edgesById.size, 0)
})

// 只看这棵子树: focusing keeps exactly the node's subtree and rebuilds the
// indexes, so panels and list views read the focused tree.
test('subtree focus keeps the branch and drops the rest', () => {
  const full = buildAssetGraphModel(
    graph({
      nodes: [
        { id: 'asset:project', kind: 'project', label: '项目' },
        { id: 'asset:row:a.example', kind: 'domain', label: 'a.example' },
        { id: 'asset:row:b.example', kind: 'domain', label: 'b.example' },
        { id: 'asset:host:h1', kind: 'host', label: 'h1' },
        { id: 'asset:service:s1', kind: 'service', label: ':443' },
      ],
      edges: [
        { id: 'e1', source: 'asset:project', target: 'asset:row:a.example', kind: 'contains' },
        { id: 'e2', source: 'asset:project', target: 'asset:row:b.example', kind: 'contains' },
        { id: 'e3', source: 'asset:row:a.example', target: 'asset:host:h1', kind: 'contains' },
        { id: 'e4', source: 'asset:host:h1', target: 'asset:service:s1', kind: 'contains' },
      ],
    }),
  )
  const focused = filterAssetModel(full, new Set(['asset:project', 'asset:row:a.example', 'asset:host:h1', 'asset:service:s1']))
  assert.deepEqual(
    focused.nodes.map((n) => n.label).sort(),
    [':443', 'a.example', 'h1', '项目'],
  )
  assert.equal(focused.edges.length, 3)
  assert.equal(focused.byId.get('asset:row:b.example'), undefined)
  assert.deepEqual(focused.incoming.get('asset:host:h1'), ['asset:row:a.example'])
  assert.ok(focused.nodes.every((n) => Number.isFinite(n.x) && Number.isFinite(n.y)))
})

test('asset kind vocabulary and missing-data fallback', () => {
  assert.ok(ASSET_FACT_KINDS.has('url'))
  assert.ok(ASSET_FACT_KINDS.has('domain'))
  assert.equal(assetKindLabel('project'), '项目')
  assert.equal(assetKindLabel('domain'), '域名')
  assert.equal(assetKindLabel('service'), '接口')
  assert.equal(assetKindLabel('url'), 'URL')
  assert.equal(assetKindLabel('mystery'), '资产')
  assert.equal(assetFenceOf({ kind: 'service' }), undefined)
  assert.equal(assetFenceOf({ kind: 'domain' }), 'unregistered')
  assert.equal(assetFenceOf({ kind: 'domain', fence: 'pending' }), 'pending')
  assert.equal(assetFenceOf({ kind: 'project' }), undefined)
  const empty = buildAssetGraphModel(undefined)
  assert.equal(empty.nodes.length, 0)
  assert.equal(empty.maxDepth, 0)
})

// A domain, a range and a host are named identities: their label belongs on
// the map at any zoom (the "信息量太少" reading). url/service stay behind
// hover — there can be many, and the hover card already names them.
test('named asset identities are labelled at every zoom; url and service are not', async () => {
  const { assetNamedKind } = await import('../src/graph/model.ts')
  assert.equal(assetNamedKind('domain'), true)
  assert.equal(assetNamedKind('range'), true)
  assert.equal(assetNamedKind('host'), true)
  assert.equal(assetNamedKind('url'), false)
  assert.equal(assetNamedKind('service'), false)
  assert.equal(assetNamedKind(undefined), false)
  // The project card and the fact kinds keep their own labelling rules.
  assert.equal(assetNamedKind('project'), false)
  assert.equal(assetNamedKind('vuln'), false)
})
