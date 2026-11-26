// The import dialog's wiring contract (spec #1 工单8/#9): the preview call must
// stay read-only, the confirm must carry the checked hosts, and the receipt
// must say how many joined the fence.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')

test('the preview step writes nothing', () => {
  const source = read('components/assets/ImportDialog.tsx')
  const loadPreview = source.slice(source.indexOf('async function loadPreview'), source.indexOf('async function confirm'))
  assert.match(loadPreview, /assetImportPreview|trafficImportPreview/)
  assert.doesNotMatch(loadPreview, /api\.importAssets|api\.importTraffic/)
})

test('the confirm carries the checked hosts and reports the fence count', () => {
  const source = read('components/assets/ImportDialog.tsx')
  assert.match(source, /api\.importTraffic\(path\.trim\(\), hosts\)/)
  assert.match(source, /api\.importAssets\(path\.trim\(\), hosts\)/)
  assert.match(source, /result\.fence_added/)
  assert.match(source, /加入围栏/)
})

test('the asset page opens the import dialog and refreshes the table', () => {
  const page = read('views/Assets.tsx')
  assert.match(page, /<ImportDialog/)
  assert.match(page, /table\.reload\(\)/)
})
