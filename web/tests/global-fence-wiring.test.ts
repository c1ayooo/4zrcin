// The instance fence page's wiring (spec #1 工单3/#9): the nav entry exists, the
// dangerous direction goes through the confirmation, the audit is read from
// the instance endpoint with its filters, and node coverage never renders as
// "covered" when the control plane cannot verify it.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')

test('the global rail has the fence as its own page', () => {
  const shell = read('views/GlobalShell.tsx')
  assert.match(shell, /id: 'fence', label: '围栏'/)
  assert.match(shell, /fence: \(\) => import\('@\/views\/Fence'\)/)
  assert.match(shell, /<Fence \/>/)
})

test('enabling a list with rows asks first, disabling does not', () => {
  const page = read('views/Fence.tsx')
  assert.match(page, /needsEnableConfirm\(draft\.commands, next\)/)
  assert.match(page, /needsEnableConfirm\(draft\.domains, next\)/)
  assert.match(page, /useConfirm\(\)/)
  assert.match(page, /destructive: true/)
  // The switch itself calls the toggle which owns the confirmation; there is
  // no second write path that skips it.
  assert.equal(page.match(/api\.saveGlobalFence/g)?.length, 1)
})

test('the audit reads the instance endpoint with source and action filters', () => {
  const page = read('views/Fence.tsx')
  assert.match(page, /api\.fenceHits\(\{ source: source \|\| undefined, action: action \|\| undefined/)
  assert.match(page, /aria-label={`按\$\{label\}筛选`}/)
})

test('node coverage is stated honestly', () => {
  const page = read('views/Fence.tsx')
  assert.match(page, /无法确认/)
  assert.match(page, /补丁 31/)
  assert.match(page, /可能未覆盖/)
})
