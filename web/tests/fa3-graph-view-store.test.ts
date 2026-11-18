// FA3: the dock (and the other right-hand overlays) size themselves against the
// map's width, which the canvas publishes to the view-store. That only works if
// subscribing to the width does not also subscribe to the camera: a pan fires a
// publish per animation frame, and re-rendering the dock on every one of those
// would undo the optimisation the store exists for. `useViewWidth()` therefore
// returns a primitive snapshot, and `useSyncExternalStore`'s `Object.is`
// comparison keeps camera-only publishes from waking it.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import { DOCK_MAX_WIDTH, DOCK_MIN_WIDTH, clampDockWidth, dockBounds } from '../src/components/graph/dock-width.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let store: any
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  store = await server.ssrLoadModule('/src/graph/view-store.ts')
})

after(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
  await server?.close()
})

test('a camera-only publish does not re-render the width consumer', async () => {
  let renders = 0
  let width = -1
  function Probe() {
    width = store.useViewWidth()
    renders++
    return null
  }
  await act(async () => {
    renderer = create(React.createElement(Probe))
  })
  const widthBefore = width
  assert.ok(widthBefore >= 0, 'the consumer sees the published width (0 before the first paint)')

  // A pan: the camera changes, the canvas size does not. This is the hot path.
  const rendersBefore = renders
  await act(async () => {
    store.setView({ camera: { x: 120, y: -40, k: 1.25 }, width: widthBefore, height: 900 })
  })
  assert.equal(renders, rendersBefore, 'panning must not re-render the dock sizing')

  // A resize: the width changes, so the consumer must wake up once.
  await act(async () => {
    store.setView({ camera: { x: 120, y: -40, k: 1.25 }, width: widthBefore + 240, height: 900 })
  })
  assert.equal(renders, rendersBefore + 1, 'a width change re-renders the consumer exactly once')
  assert.equal(width, widthBefore + 240)

  // Publishing the same width again stays quiet.
  const afterResize = renders
  await act(async () => {
    store.setView({ camera: { x: 0, y: 0, k: 2 }, width: widthBefore + 240, height: 900 })
  })
  assert.equal(renders, afterResize)
})

test('the dock is clamped against the measured map, not the window', () => {
  // Pure arithmetic, but the *input* is the contract this pass changed: the
  // canvas publishes the map width, so a 1024px window with the rail open maps
  // to ~792px of stage and a 760px dock would leave almost no map at all.
  // 792 - 24 (edge gaps) - 320 (minimum map width) = 448.
  assert.deepEqual(dockBounds(792), { min: DOCK_MIN_WIDTH, max: 448 })
  assert.equal(clampDockWidth(DOCK_MAX_WIDTH, 792), 448)
  // Before the first canvas paint the published width is 0: fall back to the
  // absolute cap instead of clamping every dock to the minimum.
  assert.deepEqual(dockBounds(0), { min: DOCK_MIN_WIDTH, max: DOCK_MAX_WIDTH })
})
