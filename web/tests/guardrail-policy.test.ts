import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  guardrailFailOpenText,
  guardrailLLMLabel,
  guardrailPolicyFlags,
  guardrailProviderLabel,
  guardrailScopeLabel,
} from '../src/lib/settings.ts'
import type { SettingsGuardrailSummary } from '../src/lib/types.ts'

// ---- pure rules --------------------------------------------------------------

test('guardrail policy flags keep "not enabled" neutral and fail-open explicit', () => {
  const disabled = guardrailPolicyFlags({ configured: false, provider: 'none' })
  assert.deepEqual(disabled, { enabled: false, failOpen: false, shadow: false, label: '守门未启用' })
  // configured=true with provider none is still "not enabled": credentials alone
  // do not enable a policy.
  assert.equal(guardrailPolicyFlags({ configured: true, provider: 'none' }).enabled, false)
  assert.equal(guardrailPolicyFlags({ configured: true, provider: '', on_error: 'review' }).enabled, false)
  const live = guardrailPolicyFlags({ configured: true, provider: 'hybrid', on_error: 'allow', shadow: true })
  assert.deepEqual(live, { enabled: true, failOpen: true, shadow: true, label: '守门已启用' })
  assert.match(guardrailFailOpenText, /守门未生效时自动放行（fail-open）/)
  assert.match(guardrailFailOpenText, /不等于已判定通过/)
})

test('provider/scope/LLM labels stay human and never leak a key', () => {
  assert.match(guardrailProviderLabel('none'), /不做自动判定/)
  assert.match(guardrailProviderLabel('jev'), /两阶段/)
  assert.match(guardrailProviderLabel('llm'), /小模型/)
  assert.match(guardrailProviderLabel('hybrid'), /REVIEW 再交 JEV/)
  assert.match(guardrailProviderLabel(undefined), /等同 none/)
  assert.equal(guardrailScopeLabel([]), '全部工具')
  assert.equal(guardrailScopeLabel(['bash', ' subagent ']), 'bash、subagent')
  assert.equal(guardrailLLMLabel(undefined), '未配置')
  assert.equal(guardrailLLMLabel({ configured: false, model: 'x' }), '未配置')
  assert.equal(guardrailLLMLabel({ configured: true, model: 'judge-1', base_url: 'http://llm/v1' }), '已配置 · judge-1 · http://llm/v1')
})

// ---- render: the three operator-relevant combinations ------------------------

let server: Awaited<ReturnType<typeof createTestServer>>
let GuardrailPolicyCard: React.ComponentType<any>
let renderer: ReactTestRenderer | undefined

test('guardrail policy card renders not-enabled, fail-open+shadow and normal LLM states', async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GuardrailPolicyCard = (await server.ssrLoadModule('/src/components/GuardrailPolicyCard.tsx')).GuardrailPolicyCard
  try {
    const render = async (guardrail?: SettingsGuardrailSummary) => {
      await act(async () => {
        renderer = create(React.createElement(GuardrailPolicyCard, { guardrail }))
      })
      return JSON.stringify(renderer!.toJSON())
    }

    // 1) provider none: neutral "not enabled", no fail-open alarm and no badge.
    const none = await render({ configured: false, provider: 'none', mode: '', on_error: 'review', shadow: false, scope: [], allow_prefixes_count: 0, jev_configured: false })
    assert.ok(none.includes('守门未启用'), none)
    assert.ok(!none.includes('自动放行（fail-open）'), none)
    assert.ok(!none.includes('影子模式'), none)
    assert.ok(!none.includes('text-destructive'), none)
    assert.ok(none.includes('bg-muted'), none)
    assert.ok(none.includes('全部工具'), none)

    // 2) on_error=allow + shadow: fail-open callout and shadow badge are both
    //    required, plus the scope/JEV/LLM summary.
    const failOpen = await render({
      configured: true, provider: 'hybrid', mode: 'safe', on_error: 'allow', shadow: true,
      scope: ['bash', 'subagent'], allow_prefixes_count: 2, jev_configured: true,
      llm: { configured: true, model: 'judge-1', base_url: 'http://llm/v1' },
    })
    assert.ok(failOpen.includes('守门已启用'), failOpen)
    assert.ok(failOpen.includes('守门未生效时自动放行（fail-open）'), failOpen)
    assert.ok(failOpen.includes('影子模式：只判定不拦截'), failOpen)
    assert.ok(failOpen.includes('bash、subagent'), failOpen)
    assert.ok(failOpen.includes('按前缀直接放行'), failOpen)
    assert.ok(failOpen.includes('"2"'), failOpen)
    assert.ok(failOpen.includes('judge-1'), failOpen)
    assert.ok(failOpen.includes('已配置'), failOpen)

    // 3) a normal LLM judge: no fail-open, no shadow, enforcement on.
    const normal = await render({
      configured: true, provider: 'llm', mode: 'strict', on_error: 'block', shadow: false,
      scope: [], allow_prefixes_count: 0, jev_configured: false,
      llm: { configured: true, model: 'judge-2' },
    })
    assert.ok(normal.includes('守门已启用'), normal)
    assert.ok(!normal.includes('自动放行（fail-open）'), normal)
    assert.ok(!normal.includes('影子模式'), normal)
    assert.ok(normal.includes('judge-2'), normal)

    // 4) an older server without the field renders nothing instead of a guess.
    const absent = await render(undefined)
    assert.equal(absent, 'null')
  } finally {
    await act(async () => { renderer?.unmount() })
    await server.close()
  }
})
