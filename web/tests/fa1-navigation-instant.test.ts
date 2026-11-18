// The route change is the contract; the view transition is an enhancement on
// top of it. These cases pin both halves: the plain path must keep working when
// the API or motion is unavailable, and the enhanced path must change history
// exactly once and only after the DOM settled.
import { afterEach, test } from 'node:test'
import assert from 'node:assert/strict'
import { navigate } from '../src/lib/navigation.ts'

const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
const originalRAF = Object.getOwnPropertyDescriptor(globalThis, 'requestAnimationFrame')

interface Harness {
  calls: { mode: string; url: string }[]
  events: number
  transitions: number
}

function harness(options: { viewTransition?: boolean; throws?: boolean; reducedMotion?: boolean } = {}): Harness {
  const state: Harness = { calls: [], events: 0, transitions: 0 }
  const location = { pathname: '/g/', search: '', hash: '' }
  const events = new EventTarget()
  const update = (mode: string, url: string) => {
    state.calls.push({ mode, url })
    const next = new URL(url, 'http://localhost')
    Object.assign(location, { pathname: next.pathname, search: next.search, hash: next.hash })
  }
  const fakeWindow = {
    location,
    history: { pushState: (_s: unknown, _t: string, url: string) => update('push', url), replaceState: (_s: unknown, _t: string, url: string) => update('replace', url) },
    matchMedia: () => ({ matches: options.reducedMotion === true }),
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    dispatchEvent: (event: Event) => {
      if (event.type === 'tw:navigate') state.events++
      return events.dispatchEvent(event)
    },
  }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: fakeWindow })
  const fakeDocument = options.viewTransition
    ? {
        startViewTransition(callback: () => void | Promise<void>) {
          state.transitions++
          if (options.throws) throw new Error('transition unavailable')
          void callback()
          return { finished: Promise.resolve() }
        },
      }
    : {}
  Object.defineProperty(globalThis, 'document', { configurable: true, value: fakeDocument })
  Object.defineProperty(globalThis, 'requestAnimationFrame', {
    configurable: true,
    value: (callback: FrameRequestCallback) => { callback(0); return 1 },
  })
  return state
}

afterEach(() => {
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  if (originalRAF) Object.defineProperty(globalThis, 'requestAnimationFrame', originalRAF)
  else Reflect.deleteProperty(globalThis, 'requestAnimationFrame')
})

test('without the View Transitions API the route still changes history and notifies readers', () => {
  const state = harness()
  navigate('/g/?view=settings')
  assert.equal(state.transitions, 0)
  assert.deepEqual(state.calls, [{ mode: 'push', url: '/g/?view=settings' }])
  assert.equal(state.events, 1)
})

test('a supported browser wraps the route change in one view transition', async () => {
  const state = harness({ viewTransition: true })
  navigate('/g/?view=projects', true)
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(state.transitions, 1)
  assert.deepEqual(state.calls, [{ mode: 'replace', url: '/g/?view=projects' }])
  assert.equal(state.events, 1)
})

test('reduced motion skips the transition but never the navigation', () => {
  const state = harness({ viewTransition: true, reducedMotion: true })
  navigate('/g/?view=kb')
  assert.equal(state.transitions, 0)
  assert.deepEqual(state.calls, [{ mode: 'push', url: '/g/?view=kb' }])
  assert.equal(state.events, 1)
})

test('a transition call that throws still commits the navigation exactly once', () => {
  const state = harness({ viewTransition: true, throws: true })
  navigate('/g/?view=pocs')
  assert.equal(state.calls.length, 1)
  assert.equal(state.events, 1)
})
