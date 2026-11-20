// The chain view's first-view rule (graph/chain-view.ts).
//
// The project brief's closure is 463 of 467 nodes: opening that as a closure
// lights the map and answers nothing, so a closure past the cap opens on the main
// path instead. The cap itself must stay a number the operator can read off the
// strip — it is not a hidden truncation.
import { test } from 'node:test'
import assert from 'node:assert/strict'
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
