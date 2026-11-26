// The runtime group's tab contract: every tab the container declares must be
// reachable — by click and by deep link (`?tab=`).
//
// Regression: the 代理 tab was declared in ExecutionEnv but missing from the
// shell's own tab whitelist (GraphView), so the click fell through selectTab's
// guard silently and an explicit `?tab=proxy` was rewritten back to 节点. The
// container compiles the union; the router keeps a second list — this test
// renders the real shell so a divergence goes red instead of silently dead.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let renderer: ReactTestRenderer | undefined
let GraphView: any, ConfirmProvider: any
const storage = new Map<string, string>()
const timers = new Map<number, () => void>()
let timerID = 0
const history: { mode: string; url: string }[] = []
const originals = {
  window: Object.getOwnPropertyDescriptor(globalThis, 'window'),
  document: Object.getOwnPropertyDescriptor(globalThis, 'document'),
  form: Object.getOwnPropertyDescriptor(globalThis, 'HTMLFormElement'),
  fetch: Object.getOwnPropertyDescriptor(globalThis, 'fetch'),
}

before(async () => {
  server = await createTestServer({
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [{
      name: 'zrcin-test:runtime-panels',
      enforce: 'pre',
      transform(_, id) {
        // The sibling panels are off the click path (the shell + TabBar own it)
        // and each pulls its own polls/terminals; a stub keeps the loop on the
        // routing seam.
        if (id.endsWith('/src/views/Nodes.tsx')) return `export const Nodes = () => null`
        if (id.endsWith('/src/views/Terminal.tsx')) return `export const TerminalView = () => null\nexport const ReplView = () => null`
        if (id.endsWith('/src/views/Shells.tsx')) return `export const Shells = () => null`
        if (id.endsWith('/src/views/Toolbox.tsx')) return `export const Toolbox = () => null`
      },
    }],
  })
  GraphView = (await server.ssrLoadModule('/src/views/GraphView.tsx')).GraphView
  ConfirmProvider = (await server.ssrLoadModule('@cyber/ui')).ConfirmProvider

  const listeners = new Map<string, Set<() => void>>()
  const applyURL = (url: string) => {
    const next = new URL(url, 'http://console.test')
    Object.assign(window.location, { pathname: next.pathname, search: next.search, hash: next.hash })
  }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: {
    location: { pathname: '/p/alpha/', search: '?view=runtime&tab=nodes', hash: '', origin: 'http://console.test' },
    history: {
      pushState(_s: unknown, _t: string, url: string) { history.push({ mode: 'push', url }); applyURL(url) },
      replaceState(_s: unknown, _t: string, url: string) { history.push({ mode: 'replace', url }); applyURL(url) },
    },
    sessionStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value) },
    },
    setInterval: (fn: () => void) => { timers.set(++timerID, fn); return timerID },
    clearInterval: (id: number) => { timers.delete(id) },
    setTimeout, clearTimeout,
    addEventListener(type: string, fn: () => void) {
      const set = listeners.get(type) ?? new Set<() => void>()
      set.add(fn)
      listeners.set(type, set)
    },
    removeEventListener(type: string, fn: () => void) { listeners.get(type)?.delete(fn) },
    dispatchEvent(event: { type: string }) { for (const fn of listeners.get(event.type) ?? []) fn(); return true },
  } })
  Object.defineProperty(globalThis, 'document', { configurable: true, value: {
    visibilityState: 'visible', addEventListener() {}, removeEventListener() {},
  } })
  Object.defineProperty(globalThis, 'HTMLFormElement', { configurable: true, value: class {} })
  globalThis.fetch = (async (url: unknown) => {
    const path = new URL(String(url), 'http://console.test').pathname
    const body = path === '/api/settings' ? { node_proxy: { proxy: '', configured: false } } : {}
    return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })
  }) as typeof fetch
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
  storage.clear(); timers.clear(); history.length = 0
})

after(async () => {
  for (const [key, name] of [['window', 'window'], ['document', 'document'], ['form', 'HTMLFormElement'], ['fetch', 'fetch']] as const) {
    const original = originals[key]
    if (original) Object.defineProperty(globalThis, name, original)
    else Reflect.deleteProperty(globalThis, name)
  }
  await server?.close()
})

/** Render the real shell on the runtime group and wait for the tab strip. */
async function openRuntime(search: string) {
  window.location.pathname = '/p/alpha/'
  window.location.search = search
  window.location.hash = ''
  await act(async () => {
    renderer = create(React.createElement(ConfirmProvider, null,
      React.createElement(GraphView, { current: 'alpha', projects: [], onRefresh() {} })))
  })
  await settle(() => Boolean(tabButton('tab-proxy')), 'the tab strip')
  return tabButton('tab-proxy')
}

async function settle(check: () => boolean, label: string, ms = 8000) {
  const until = Date.now() + ms
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${label}`)
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)) })
  }
}

const tabButton = (id: string) => renderer!.root.findAllByProps({ 'data-testid': id })[0]

test('a declared tab switches on click and lands in the URL (control: 工具与能力)', async () => {
  await openRuntime('?view=runtime&tab=nodes')
  const before = history.length
  await act(async () => { tabButton('tab-toolbox').props.onClick() })
  assert.equal(window.location.search, '?view=runtime&tab=toolbox')
  assert.equal(history.length, before + 1, 'one history entry for the tab switch')
  assert.equal(tabButton('tab-toolbox').props['aria-selected'], true)
})

test('the 代理 tab opens on click: URL, history and selection all move together', async () => {
  await openRuntime('?view=runtime&tab=nodes')
  const proxy = tabButton('tab-proxy')
  assert.equal(proxy.type, 'button')
  assert.notEqual(proxy.props.disabled, true)
  const before = history.length
  await act(async () => { proxy.props.onClick() })
  assert.equal(window.location.search, '?view=runtime&tab=proxy', 'the address bar must name the proxy tab')
  assert.equal(history.length, before + 1)
  assert.equal(tabButton('tab-proxy').props['aria-selected'], true)
  // The panel is the operator-visible outcome: the node default proxy card.
  await settle(() => renderer!.root.findAllByProps({ 'aria-label': '节点出网代理地址' }).length > 0, 'the proxy card')
})

test('an explicit ?tab=proxy deep link opens the proxy tab', async () => {
  const proxy = await openRuntime('?view=runtime&tab=proxy')
  assert.equal(window.location.search, '?view=runtime&tab=proxy')
  assert.equal(proxy.props['aria-selected'], true, 'an explicit ?tab= must win over the group default')
})

test('the WebShell tab opens on click and survives a deep link', async () => {
  await openRuntime('?view=runtime&tab=nodes')
  const before = history.length
  await act(async () => { tabButton('tab-shells').props.onClick() })
  assert.equal(window.location.search, '?view=runtime&tab=shells')
  assert.equal(history.length, before + 1)
  assert.equal(tabButton('tab-shells').props['aria-selected'], true)

  await openRuntime('?view=runtime&tab=shells')
  assert.equal(window.location.search, '?view=runtime&tab=shells')
  assert.equal(tabButton('tab-shells').props['aria-selected'], true, 'an explicit ?tab=shells must win over the group default')
})
