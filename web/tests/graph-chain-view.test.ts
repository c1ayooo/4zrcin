// The chain view's first-view rule (graph/chain-view.ts).
//
// The project brief's closure is 463 of 467 nodes: opening that as a closure
// lights the map and answers nothing, so a closure past the cap opens on the main
// path instead. The cap itself must stay a number the operator can read off the
// strip — it is not a hidden truncation.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { CHAIN_CAP, defaultChainMode } from '../src/graph/chain-view.ts'

test('a chain under the cap opens as the closure', () => {
  assert.equal(defaultChainMode(1), 'closure')
  assert.equal(defaultChainMode(10), 'closure')
  assert.equal(defaultChainMode(CHAIN_CAP - 1), 'closure')
})

test('the cap itself still opens as the closure', () => {
  assert.equal(defaultChainMode(CHAIN_CAP), 'closure')
})

test('past the cap the main path is the first view', () => {
  assert.equal(defaultChainMode(CHAIN_CAP + 1), 'path')
  assert.equal(defaultChainMode(253), 'path')
  // The project brief in the sampled project.
  assert.equal(defaultChainMode(463), 'path')
})

// A click on a node used to install the path filter by itself (a closure past the
// cap opened as its main path and filtered the map), which read as "clicking a
// node jumps me into a chain". The rule stays available to the strip's explicit
// 看完整 / 只看主路径, but only that action may filter the map.
test('selecting a node never installs the chain filter by itself', () => {
  const source = readFileSync(
    fileURLToPath(new URL('../src/components/graph/GraphPanel.tsx', import.meta.url)),
    'utf8',
  )
  assert.equal(
    (source.match(/setPathSet\(chain\.pathSet\)/g) ?? []).length,
    1,
    'only the explicit chain action installs the path filter',
  )
  assert.equal(
    (source.match(/focusOnly: true/g) ?? []).length,
    1,
    'only the explicit chain action turns focusOnly on',
  )
})
