// The review panel has two independent switches: the temporary pause (resume)
// and the runtime close (`disabled`, --no-verify at boot or 关闭复核). A closed
// review can never run, so the panel may not offer resume; it must say so and
// offer opening instead, and a resume that comes back 409 `verify_disabled` from a
// stale page must read as exactly that instead of a generic failure.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let renderer: ReactTestRenderer | undefined
let Vulns: any, api: any, ApiError: any
const timers = new Map<number, () => void>()
let timerID = 0
const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
const oldDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
const oldFormElement = Object.getOwnPropertyDescriptor(globalThis, 'HTMLFormElement')

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  Vulns = (await server.ssrLoadModule('/src/views/Vulns.tsx')).Vulns
  const client = await server.ssrLoadModule('/src/lib/api.ts')
  api = client.api
  ApiError = client.ApiError
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: { origin: 'http://localhost', pathname: '/p/alpha/', search: '', hash: '' },
      sessionStorage: { getItem: () => null, setItem: () => {} },
      setInterval: (fn: () => void) => {
        timers.set(++timerID, fn)
        return timerID
      },
      clearInterval: (id: number) => void timers.delete(id),
      setTimeout,
      clearTimeout,
      addEventListener() {},
      removeEventListener() {},
    },
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: { visibilityState: 'visible', addEventListener() {}, removeEventListener() {} },
  })
  Object.defineProperty(globalThis, 'HTMLFormElement', { configurable: true, value: class {} })
})

afterEach(async () => {
  await act(async () => {
    renderer?.unmount()
    renderer = undefined
  })
  timers.clear()
})

after(async () => {
  if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
  else Reflect.deleteProperty(globalThis, 'window')
  if (oldDocument) Object.defineProperty(globalThis, 'document', oldDocument)
  else Reflect.deleteProperty(globalThis, 'document')
  if (oldFormElement) Object.defineProperty(globalThis, 'HTMLFormElement', oldFormElement)
  else Reflect.deleteProperty(globalThis, 'HTMLFormElement')
  await server.close()
})

const vuln = (i = 1) => ({
  id: `vuln_${i}`,
  project_id: 'alpha',
  source_id: `fact_${i}`,
  title: `结论 ${i}`,
  summary: '摘要',
  status: 'unverified',
  severity: 'high',
  created_at: '2026-11-15T00:00:00Z',
  evidence: [],
})

async function mount(status: Record<string, unknown>, vulns: unknown[] = []) {
  api.vulns = async () => ({ vulns, total: vulns.length, matched: vulns.length })
  api.verifierStatus = async () => status
  await act(async () => {
    renderer = create(React.createElement(Vulns, { lockedProject: 'alpha' }))
  })
}

/** Text content of a host node (icons render as elements, so walk children). */
function textOf(node: any): string {
  const parts: string[] = []
  const walk = (child: any) => {
    if (child === null || child === undefined || typeof child === 'boolean') return
    if (typeof child === 'string' || typeof child === 'number') {
      parts.push(String(child))
      return
    }
    if (Array.isArray(child)) {
      child.forEach(walk)
      return
    }
    if (child.props) walk(child.props.children)
  }
  walk(node?.props?.children)
  return parts.join('')
}

/** Find a host <button> whose visible label is exactly `label`. */
function button(label: string) {
  return renderer!.root.findAllByType('button').find((node) => textOf(node) === label)
}

function tree(): string {
  return JSON.stringify(renderer!.toJSON())
}

test('a closed review shows the closure notice and offers opening instead of resuming', async () => {
  await mount({ enabled: false, paused: true, disabled: true }, [vuln()])

  assert.match(tree(), /复核已关闭：不会再有复核 run，未核验事实按原文可见/)
  assert.ok(button('开启复核'), 'closing must leave a way back in')
  assert.equal(button('继续复核'), undefined, 'a closed review has nothing to resume')
  assert.equal(button('暂停复核'), undefined)
  assert.equal(button('一键复核'), undefined)

  // The row action stays visible but disabled, with the reason on the control:
  // a dead "复核" button that looked clickable would promise a run that cannot
  // happen, and a title pointing at "继续复核" would point at nothing.
  const rowVerify = button('复核')
  assert.ok(rowVerify, 'the row action is disabled, not silently removed')
  assert.equal(rowVerify!.props.disabled, true)
  assert.match(String(rowVerify!.props.title), /复核已关闭/)
  const caption = renderer!.root
    .findAllByType('span')
    .find((node) => textOf(node) === '复核已关闭')
  assert.ok(caption, 'the row states the reason visibly instead of only in a tooltip')

  let enables = 0
  api.enableReviews = async () => {
    enables++
    return { disabled: false }
  }
  api.verifierStatus = async () => ({ enabled: true, paused: false, disabled: false })
  await act(async () => {
    button('开启复核')!.props.onClick()
  })
  assert.equal(enables, 1, 'opening reviews calls the enable endpoint')
  assert.ok(button('暂停复核'), 'an opened review is back to the pause/resume switch')
  assert.equal(button('开启复核'), undefined)
})

test('a paused review offers 继续复核 and calls resume', async () => {
  await mount({ enabled: true, paused: true, disabled: false })

  assert.ok(button('继续复核'), 'a temporary pause is resumable')
  assert.ok(button('关闭复核'), 'the runtime close stays available next to the pause')
  let resumes = 0
  api.resumeReviews = async () => {
    resumes++
    return { paused: false }
  }
  api.verifierStatus = async () => ({ enabled: true, paused: false, disabled: false })
  await act(async () => {
    button('继续复核')!.props.onClick()
  })
  assert.equal(resumes, 1)
  assert.ok(button('暂停复核'), 'resuming returns to the normal state')
})

test('a 409 verify_disabled from resume is explained and re-reads the switch', async () => {
  await mount({ enabled: true, paused: true, disabled: false })

  api.resumeReviews = async () => {
    throw new ApiError(409, '本实例以 --no-verify 启动：没有复核器可恢复', 'verify_disabled', {
      error: '本实例以 --no-verify 启动：没有复核器可恢复',
      code: 'verify_disabled',
    })
  }
  // The status was stale: re-reading it shows the review is closed now.
  api.verifierStatus = async () => ({ enabled: false, paused: true, disabled: true })
  await act(async () => {
    button('继续复核')!.props.onClick()
  })

  const current = tree()
  assert.match(current, /无法恢复/)
  assert.match(current, /开启复核/, 'the panel switches to the way that can work')
  assert.doesNotMatch(current, /切换复核状态失败/, 'a closed review is not a transport failure')
})

test('closing reviews calls the disable endpoint and reports canceled runs', async () => {
  await mount({ enabled: true, paused: false, disabled: false })

  let closes = 0
  api.disableReviews = async () => {
    closes++
    return { disabled: true, canceled: 2 }
  }
  api.verifierStatus = async () => ({ enabled: false, paused: true, disabled: true })
  await act(async () => {
    button('关闭复核')!.props.onClick()
  })

  assert.equal(closes, 1)
  const current = tree()
  assert.match(current, /正在跑的 2 条复核已取消/)
  assert.ok(button('开启复核'))
  assert.equal(button('暂停复核'), undefined)
})
