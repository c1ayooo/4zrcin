import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  fenceStateOf,
  fenceVariant,
  filterAssetRows,
  kindLabel,
  statusSummary,
  testedOf,
} from '../src/lib/assets.ts'
import type { AssetRow } from '../src/lib/types.ts'

function row(over: Partial<AssetRow>): AssetRow {
  return {
    key: 'example.com',
    kind: 'domain',
    hosts: ['www.example.com'],
    host_count: 1,
    urls: 2,
    requests: 3,
    vulns: 0,
    tested: 'reached',
    ...over,
  }
}

// The fence reading is the asset's standing or "未登记" — never "out of
// fence": a row with no asset record says exactly that.
test('fence state classifies stored standing and the unregistered case', () => {
  assert.equal(fenceStateOf({ fence: 'in_fence' }), 'in_fence')
  assert.equal(fenceStateOf({ fence: 'pending' }), 'pending')
  assert.equal(fenceStateOf({ fence: 'denied' }), 'denied')
  assert.equal(fenceStateOf({}), 'unregistered')
  assert.equal(fenceStateOf({ fence: 'something-new' }), 'unregistered')
  assert.equal(fenceVariant('pending'), 'warning')
  assert.equal(fenceVariant(undefined), 'muted')
})

// The three states are the server's derivation, not a client upgrade:
// untouched / reached / concluded, unknown falls back to untouched.
test('tested labels stay three-state', () => {
  assert.equal(testedOf({ tested: 'untouched' }).label, '未触达')
  assert.equal(testedOf({ tested: 'reached' }).label, '已触达')
  assert.equal(testedOf({ tested: 'concluded' }).label, '有结论')
  assert.equal(testedOf({ tested: 'weird' }).label, '未知')
})

test('status summary is bucketed and ordered', () => {
  assert.equal(statusSummary({ '4xx': 3, '2xx': 12, error: 1 }), '2xx 12 · 4xx 3 · error 1')
  assert.equal(statusSummary({}), '')
  assert.equal(statusSummary(undefined), '')
})

// Search covers the row key and its observed hosts; the fence chip narrows
// independently; both compose.
test('row filtering composes query and fence', () => {
  const rows = [
    row({ key: 'example.com', hosts: ['www.example.com', 'api.example.com'], fence: 'in_fence' }),
    row({ key: 'pending.example', hosts: ['pending.example'], fence: 'pending' }),
    row({ key: '10.0.0.5', kind: 'host', hosts: ['10.0.0.5'] }),
  ]
  assert.equal(filterAssetRows(rows, { query: '', fence: '' }).length, 3)
  assert.deepEqual(
    filterAssetRows(rows, { query: '', fence: 'pending' }).map((r) => r.key),
    ['pending.example'],
  )
  assert.deepEqual(
    filterAssetRows(rows, { query: 'API', fence: '' }).map((r) => r.key),
    ['example.com'],
  )
  assert.deepEqual(
    filterAssetRows(rows, { query: 'example', fence: 'in_fence' }).map((r) => r.key),
    ['example.com'],
  )
  assert.equal(filterAssetRows(rows, { query: 'nothing', fence: '' }).length, 0)
})

test('kind labels distinguish ranges and IPs from domains', () => {
  assert.equal(kindLabel('domain'), '域名')
  assert.equal(kindLabel('host'), 'IP')
  assert.equal(kindLabel('range'), '网段')
  assert.equal(kindLabel('service'), '端口')
})
