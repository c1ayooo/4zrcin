import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  groupImportPreview,
  importConfirmProblem,
  importKindLabel,
  isSelectable,
  selectAllOutOfFence,
  selectionImpact,
  toggleImportHost,
} from '../src/lib/import-preview.ts'
import type { ImportPreview, ImportPreviewItem } from '../src/lib/types.ts'

function item(over: Partial<ImportPreviewItem>): ImportPreviewItem {
  return { host: 'a.example', kind: 'domain', classification: 'out_of_fence', ...over }
}

const preview: ImportPreview = {
  kind: 'assets',
  path: 'scan.jsonl',
  records: 9,
  hosts: 4,
  in_fence: 1,
  out_of_fence: 2,
  unjudged: 1,
  fence_enabled: true,
  items: [
    item({ host: 'in.example', classification: 'in_fence' }),
    item({ host: 'out1.example', ports: 2 }),
    item({ host: 'out2.example', ports: 1 }),
    item({ host: 'unknown.example', classification: 'unjudged' }),
  ],
}

// Three groups, and an unexpected value reads as 无法判定 — never silently as
// "in range".
test('preview grouping is exactly three groups', () => {
  const groups = groupImportPreview(preview)
  assert.deepEqual(groups.inFence.map((i) => i.host), ['in.example'])
  assert.deepEqual(groups.outOfFence.map((i) => i.host), ['out1.example', 'out2.example'])
  assert.deepEqual(groups.unjudged.map((i) => i.host), ['unknown.example'])
  const odd = groupImportPreview({ ...preview, items: [{ ...item({}), classification: 'weird' as never }] })
  assert.equal(odd.unjudged.length, 1)
  assert.equal(groupImportPreview(undefined).outOfFence.length, 0)
})

test('only out-of-fence hosts are selectable', () => {
  assert.equal(isSelectable(item({})), true)
  assert.equal(isSelectable(item({ classification: 'in_fence' })), false)
  assert.equal(isSelectable(item({ classification: 'unjudged' })), false)
  const selected = new Set<string>()
  const one = toggleImportHost(selected, 'out1.example', true)
  assert.deepEqual([...one], ['out1.example'])
  assert.deepEqual([...toggleImportHost(one, 'out1.example', true)], [])
  // A non-selectable host cannot be toggled in at all.
  assert.deepEqual([...toggleImportHost(one, 'in.example', false)], ['out1.example'])
})

test('select all toggles the out-of-fence group and leaves the rest alone', () => {
  const groups = groupImportPreview(preview)
  const all = selectAllOutOfFence(groups, new Set())
  assert.deepEqual([...all].sort(), ['out1.example', 'out2.example'])
  const cleared = selectAllOutOfFence(groups, all)
  assert.equal(cleared.size, 0)
  const partial = selectAllOutOfFence(groups, new Set(['out1.example']))
  assert.deepEqual([...partial].sort(), ['out1.example', 'out2.example'])
})

test('impact counts only selected out-of-fence hosts and the batch it will import', () => {
  const selected = new Set(['out1.example', 'in.example', 'unknown.example'])
  const impact = selectionImpact(preview, selected)
  assert.deepEqual(impact.fenceHosts, ['out1.example'])
  assert.equal(impact.fenceCount, 1)
  assert.equal(impact.records, 9)
  assert.equal(impact.totalHosts, 4)
  assert.equal(impact.allSelected, false)
  const all = selectionImpact(preview, new Set(['out1.example', 'out2.example']))
  assert.equal(all.allSelected, true)
  assert.equal(selectionImpact(undefined, new Set()).records, 0)
  assert.equal(importKindLabel('traffic'), 'HAR 流量')
  assert.equal(importKindLabel('assets'), '扫描结果')
})

test('confirm is refused without a preview or a path', () => {
  assert.match(importConfirmProblem(undefined, 'x', false) ?? '', /预览/)
  assert.match(importConfirmProblem(preview, '  ', false) ?? '', /路径/)
  assert.match(importConfirmProblem(preview, 'scan.jsonl', true) ?? '', /导入/)
  assert.equal(importConfirmProblem(preview, 'scan.jsonl', false), undefined)
})
