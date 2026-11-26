// The asset tree's wiring contract (spec #1 工单7-9/#8, 对齐后的形态).
//
// These are things a pure model test cannot see: the page opens on the tree
// with the table one toggle away, there is exactly one canvas, focusing a
// domain uses the subtree mechanism with an explicit way back, leaving the
// fence goes through the confirmation that states the consequences, and the
// node panel lists the instructions bound to the asset by reference.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')

test('the asset page opens on the tree, table one toggle away', () => {
  const page = read('views/Assets.tsx')
  // Exactly one mount: both views cannot be on screen at once.
  assert.equal(page.match(/<AssetGraphPanel/g)?.length, 1)
  // The default is the graph; the table is the explicit switch.
  assert.match(page, /useState<'table' \| 'graph'>\('graph'\)/)
  assert.match(page, /value="table"/)
  assert.match(page, /value="graph"/)
  // The toggle lists 图谱 first, because it is what the page opens with.
  assert.ok(page.indexOf('value="graph"') < page.indexOf('value="table"'))
})

test('the tree reads left-to-right from a project card, no drill mode', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  const lib = read('lib/asset-graph.ts')
  // The layered layout is the aligned default.
  assert.match(panel, /useState<LayoutMode>\('layered'\)/)
  // No second view: the old full/drill props are gone.
  assert.doesNotMatch(panel, /onDrill|rowKey|查看该域名图谱/)
  // The root card is the project-shaped node the layered layout anchors on,
  // and a new tree is framed once instead of opening on a stale camera.
  assert.match(lib, /n\.id === rootID \? 'project' : 'fact'/)
  assert.match(panel, /canvas\.current\?\.fit\(\{ margin: 0\.14, capAtOne: true \}\)/)
})

test('focusing a domain uses the subtree with an explicit way back', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /subtree\(view, id\)/)
  assert.match(panel, /filterAssetModel\(model, focusIds\)/)
  assert.match(panel, /只看这棵子树/)
  assert.match(panel, /返回全图/)
})

test('leaving the fence goes through the stated consequences', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /useConfirm\(\)/)
  assert.match(panel, /停测指令/)
  assert.match(panel, /停测后/)
  assert.match(panel, /changeAssetFence\(node\.asset_id, 'leave'\)/)
})

test('the node panel lists instructions and vulnerability tags by reference', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /row\.asset_ref === assetKey/)
  assert.match(panel, /createAssetInstruction\(\{ text: body, asset_ref: assetRef/)
  // The tags ride the node payload the server computed, never a client guess.
  assert.match(panel, /node\.vuln_tags/)
})
