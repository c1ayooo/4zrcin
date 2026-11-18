// The canvas' selection-focus fade (graph/focus-fade.ts): a selection eases its
// cold dim in, but only from the dim that is actually on screen. Hover already
// dims the map, so clicking a hovered subject must not flash it back to full
// brightness and fade down again.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { FOCUS_FADE_MS, focusFadeMix, initialFocusFade } from '../src/graph/focus-fade.ts'

const targetA = new Set(['n:a', 'n:b'])
const targetB = new Set(['n:c'])

test('a selection with nothing hovered eases the dim in from full brightness', () => {
  const fade = initialFocusFade()
  const start = focusFadeMix(fade, targetA, true, 1000)
  assert.equal(start.mix, 0, 'the first frame paints the map undimmed')
  assert.equal(start.settledAt, 1000 + FOCUS_FADE_MS)
  const mid = focusFadeMix(fade, targetA, true, 1000 + FOCUS_FADE_MS / 2)
  assert.ok(mid.mix > 0 && mid.mix < 1, `mid fade = ${mid.mix}`)
  const end = focusFadeMix(fade, targetA, true, 1000 + FOCUS_FADE_MS)
  assert.equal(end.mix, 1, 'the settled frame is the full cold dim')
  assert.equal(end.settledAt, 0)
})

test('hovering publishes the dim immediately and a following click keeps it', () => {
  const fade = initialFocusFade()
  // Hover frame: no selection, but the map is already dimmed to the cold alpha.
  assert.deepEqual(focusFadeMix(fade, targetA, false, 500), { mix: 1, settledAt: 0 })
  assert.equal(fade.mix, 1, 'the on-screen dim is recorded, not "no dim"')

  // Clicking the hovered node re-renders with the selection set. If the fade
  // starts from "no dim" here the whole map flashes bright for a frame.
  const first = focusFadeMix(fade, targetA, true, 510)
  assert.equal(first.mix, 1, 'no flash: the dim continues from the hover state')
  assert.equal(first.settledAt, 0, 'nothing left to ease')

  // Switching the subject from an already dimmed frame also starts at full dim.
  const switched = focusFadeMix(fade, targetB, true, 520)
  assert.equal(switched.mix, 1, `subject switch = ${switched.mix}`)
})

test('hover takes over without a transition, and clearing restores brightness', () => {
  const fade = initialFocusFade()
  focusFadeMix(fade, targetA, true, 0)
  focusFadeMix(fade, targetA, true, FOCUS_FADE_MS)
  assert.equal(fade.mix, 1)

  // Pointer leaves / no focus at all: paint undimmed and remember that.
  assert.deepEqual(focusFadeMix(fade, null, false, 1000), { mix: 1, settledAt: 0 })
  assert.equal(fade.mix, 0, 'the next selection has to ease in from brightness')

  // A hovered subject is dimmed instantly again.
  assert.equal(focusFadeMix(fade, targetA, false, 1010).mix, 1)
  assert.equal(fade.mix, 1)
})

test('the ease is monotonic and clamped to its window', () => {
  const fade = initialFocusFade()
  focusFadeMix(fade, targetA, true, 0)
  let previous = -1
  for (let t = 0; t <= FOCUS_FADE_MS + 50; t += 10) {
    const { mix } = focusFadeMix(fade, targetA, true, t)
    assert.ok(mix >= previous, `mix went backwards at ${t}: ${mix} < ${previous}`)
    assert.ok(mix >= 0 && mix <= 1, `mix out of range at ${t}: ${mix}`)
    previous = mix
  }
  assert.equal(previous, 1, 'it settles at the cold alpha')
  // Past the window the fade is over; the frame loop may stop.
  assert.equal(focusFadeMix(fade, targetA, true, FOCUS_FADE_MS * 4).settledAt, 0)
})

test('a clock that has not advanced keeps the start frame', () => {
  const fade = initialFocusFade()
  focusFadeMix(fade, targetA, true, 700)
  const same = focusFadeMix(fade, targetA, true, 700)
  assert.equal(same.mix, 0)
  assert.equal(same.settledAt, 700 + FOCUS_FADE_MS)
})
