import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  LATEST_THRESHOLD_PX,
  scrollViewportToLatest,
  viewportAtLatest,
} from '../src/lib/latest-scroll.ts'

class FakeViewport {
  scrollHeight = 1000
  clientHeight = 400
  scrollTop = 0
  calls: ScrollToOptions[] = []
  scrollTo(options: ScrollToOptions) {
    this.calls.push(options)
    this.scrollTop = Number(options.top ?? 0)
  }
}

test('the latest position is the bottom edge within a small threshold', () => {
  const viewport = new FakeViewport()
  assert.equal(viewportAtLatest(viewport), false)
  viewport.scrollTop = 600
  assert.equal(viewportAtLatest(viewport), true)
  viewport.scrollTop = 600 - LATEST_THRESHOLD_PX
  assert.equal(viewportAtLatest(viewport), true, 'a few pixels off is still reading the newest row')
  viewport.scrollTop = 600 - LATEST_THRESHOLD_PX - 1
  assert.equal(viewportAtLatest(viewport), false)
  assert.equal(viewportAtLatest(null), true, 'an unmounted pane is not "away" from the latest row')
})

test('jumping scrolls the viewport itself and can request a smooth animation', () => {
  const viewport = new FakeViewport()
  scrollViewportToLatest(viewport)
  assert.deepEqual(viewport.calls, [{ top: 1000, behavior: 'auto' }])
  assert.equal(viewport.scrollTop, 1000)
  scrollViewportToLatest(viewport, 'smooth')
  assert.deepEqual(viewport.calls.at(-1), { top: 1000, behavior: 'smooth' })
})

test('the viewport helper tolerates an element without scrollTo (SSR test doubles)', () => {
  const viewport = { scrollHeight: 90, clientHeight: 20, scrollTop: 0 } as unknown as HTMLElement
  scrollViewportToLatest(viewport)
  assert.equal(viewport.scrollTop, 90)
})
