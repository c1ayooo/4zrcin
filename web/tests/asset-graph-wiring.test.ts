// The asset graph's wiring contract (spec #1 工单7/#8).
//
// These are things a pure model test cannot see: the page must switch between
// 表格 and 图谱 without mounting two canvases, the drill-down must centre the
// selected domain explicitly, leaving the fence must go through the
// confirmation that states the consequences, and the node panel must list the
// instructions bound to the asset by reference.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')

test('the asset page has one canvas behind a table/graph switch', () => {
  const page = read('views/Assets.tsx')
  // Exactly one mount: both views cannot be on screen at once.
  assert.equal(page.match(/<AssetGraphPanel/g)?.length, 1)
  assert.match(page, /value="table"/)
  assert.match(page, /value="graph"/)
  // The table's drill-down sheet hands the row to the graph view.
  assert.match(page, /onGraph=\{\(\) => openGraph\(selected\)\}/)
})

test('the drill-down centres the selected domain explicitly', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /canvas\.current\?\.centerOn\(root\.x, root\.y\)/)
  // The root is the project-shaped node the layered layout anchors on.
  assert.match(panel, /n\.type === 'project'/)
})

test('leaving the fence goes through the stated consequences', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /useConfirm\(\)/)
  assert.match(panel, /停测指令/)
  assert.match(panel, /停测后/)
  assert.match(panel, /changeAssetFence\(node\.asset_id, 'leave'\)/)
})

test('the node panel lists instructions by their asset reference', () => {
  const panel = read('components/assets/AssetGraphPanel.tsx')
  assert.match(panel, /row\.asset_ref === assetKey/)
  assert.match(panel, /createAssetInstruction\(\{ text: body, asset_ref: assetRef/)
})
