import { useEffect, useRef, useState } from 'react'
import { KeyRound, ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react'
import { Badge, Button, Callout, Field, Input } from '@cyber/ui'
import { Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { settingsApplyNotice, settingsModelPayload } from '@/lib/settings'
import type { AsyncState } from '@/lib/hooks'
import {
  guardrailAllowPrefixesExplainText,
  guardrailAllowPrefixesSummary,
  guardrailEnableText,
  guardrailFailOpenText,
  guardrailLLMLabel,
  guardrailLLMSemanticsText,
  guardrailPolicyFlags,
  guardrailProviderLabel,
  guardrailScopeExplainText,
  guardrailScopeSummary,
  guardrailUsesLLM,
} from '@/lib/settings'
import type { SettingsGuardrailSummary, SettingsJEVSummary, SettingsView } from '@/lib/types'

/**
 * Summary of the operator's `guardrail` policy, plus two editors: the judge
 * decision (provider/mode/on_error/shadow and the llm judge subsection) and
 * the JEV credentials (`jev`).
 *
 * scope/allow_prefixes and every other policy oddity stay read-only (the
 * operator writes them in cyber.yaml directly). The decision and the secrets
 * are console-sized choices — switching a deployment between "no judge",
 * "JEV", "a plain OpenAI-compatible model" (`provider: llm`, no JEV needed)
 * and "human review" should not be a text-editing exercise, and a key is a
 * secret the console can hold but never echo. The card reports what is in
 * effect — and, just as important, how to read it:
 *
 *  - provider none / configured=false is "守门未启用" in neutral grey — a
 *    configuration state, not an error; JEV is optional (llm-only is valid);
 *  - on_error=allow is the explicit fail-open tradeoff and must be called out
 *    prominently: a let-through is not a passed judgement (docs §10.3);
 *  - shadow=true is "只判定不拦截" — grey rollout, never enforcement;
 *  - scope is a reduction of the checked surface, NOT isolation: unlisted tools
 *    are never judged, and bash/subagent can still do the same work;
 *  - allow_prefixes is literal prefix matching in safe mode only (`ls` matches
 *    `lsof`) — a noise reducer, not a semantic firewall;
 *  - JEV/LLM show only whether they are configured (plus model/base_url), never
 *    a key.
 */
export function GuardrailPolicyCard({
  guardrail,
  jev,
  settings,
}: {
  guardrail?: SettingsGuardrailSummary
  jev?: SettingsJEVSummary
  settings: AsyncState<SettingsView>
}) {
  // An older server has no guardrail projection at all: render nothing instead
  // of inventing a "not configured" state that could be wrong.
  if (!guardrail) return null
  const flags = guardrailPolicyFlags(guardrail)
  const allowPrefixes = guardrail.allow_prefixes_count ?? 0
  const usesLLM = guardrailUsesLLM(guardrail.provider)
  return (
    <Panel
      title="守门策略"
      extra={<Badge variant="outline" size="sm">决策可改 · scope/prefixes 只读 · cyber.yaml</Badge>}
      bodyClassName="space-y-3"
      data-testid="guardrail-policy"
    >
      <div className="flex flex-wrap items-center gap-2">
        {flags.enabled ? (
          <ShieldCheck className="h-4 w-4 text-success" aria-hidden="true" />
        ) : (
          <ShieldOff className="h-4 w-4 text-ink-3" aria-hidden="true" />
        )}
        <Badge size="sm" variant={flags.enabled ? 'success' : 'muted'}>{flags.label}</Badge>
        {flags.shadow && <Badge size="sm" variant="info">影子模式：只判定不拦截</Badge>}
        <span className="text-[11px] text-ink-3">决策与凭据可在此修改；scope 等其余策略仍在 cyber.yaml 手改。</span>
      </div>

      {!flags.enabled && (
        <p className="text-[12px] leading-relaxed text-ink-2">
          守门未启用：工具调用前不会经过自动判定，也不会进入人工审批流程。
          只配置凭据不会启用策略。在下方「裁判决策」里选择 provider（provider: llm 加 llm 子段，不需要 JEV）或 jev / hybrid，重启宿主后生效。
        </p>
      )}

      {flags.failOpen && (
        <Callout tone="warning" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
          <span className="font-medium">守门未生效时自动放行（fail-open）</span>：裁判不可用、超时或输出不合法时会直接放行被拦截的工具调用。这是显式的安全取舍，放行不等于已判定通过。
        </Callout>
      )}

      <dl className="grid grid-cols-1 gap-x-4 gap-y-1.5 text-[12px] leading-relaxed sm:grid-cols-2">
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">裁判</dt>
          <dd className="zrcin-break min-w-0 text-ink-2">{guardrailProviderLabel(guardrail.provider)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">模式</dt>
          <dd className="zrcin-break min-w-0 text-ink-2">{guardrail.mode?.trim() || '默认'}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">检查面（scope）</dt>
          <dd className="zrcin-break min-w-0 text-ink-2">{guardrailScopeSummary(guardrail.scope)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">前缀放行（allow_prefixes）</dt>
          <dd className="zrcin-break min-w-0 text-ink-2">{guardrailAllowPrefixesSummary(allowPrefixes)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">异常处理（on_error）</dt>
          <dd className={`zrcin-break min-w-0 ${flags.failOpen ? 'font-medium text-warning' : 'text-ink-2'}`}>
            {guardrail.on_error?.trim() || '未设置（默认 review）'}
            {flags.failOpen ? ' · 自动放行' : guardrail.on_error === 'review' ? ' · 转人工复核' : guardrail.on_error === 'block' ? ' · 直接拦截' : ''}
          </dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">小模型裁判</dt>
          <dd className="zrcin-break min-w-0 font-mono text-[11px] text-ink-2">{guardrailLLMLabel(guardrail.llm)}</dd>
        </div>
      </dl>

      <DecisionEditor guardrail={guardrail} settings={settings} />

      <JEVEditor jev={jev} settings={settings} />

      {/* How to read the two operator-facing switches. These are the same
          caveats the harness documentation states; the card must not imply that
          scope isolates executions or that prefix matching understands shell. */}
      <div className="space-y-1.5 border-t border-hairline pt-2 text-[11px] leading-relaxed text-ink-3">
        <p className="zrcin-break">
          <span className="font-medium text-ink-2">scope（只缩小检查面）</span>：{guardrailScopeExplainText}
        </p>
        <p className="zrcin-break">
          <span className="font-medium text-ink-2">allow_prefixes（仅 safe，字面匹配）</span>：{guardrailAllowPrefixesExplainText}
        </p>
        <p className="zrcin-break">
          <span className="font-medium text-ink-2">如何启用</span>：{guardrailEnableText}
        </p>
        {usesLLM && (
          <p className="zrcin-break">
            <span className="font-medium text-ink-2">LLM 判定语义</span>：{guardrailLLMSemanticsText}
          </p>
        )}
      </div>

      {flags.failOpen && (
        <p className="text-[11px] leading-relaxed text-ink-3">{guardrailFailOpenText}</p>
      )}
    </Panel>
  )
}

/** The one editable piece of the guardrail surface: the JEV judge's
 *  credentials. The stored key never comes back to the console, so an empty
 *  key field means "keep it" and removing it is an explicit action — a save
 *  that did not touch the credential can never wipe it. Base URL and model
 *  are not secrets: empty clears them back to the engine default. The policy
 *  reads the file at scan start, so a change is restart-gated like the other
 *  product sections. */
/** The decision editor: who judges (provider), what a verdict does (mode),
 *  the fail-open stance (on_error), the grey rollout (shadow), and the llm
 *  judge subsection — the path that needs no JEV at all: any OpenAI-compatible
 *  endpoint judges. The llm key follows the keep-on-empty contract. scope and
 *  allow_prefixes stay read-only: they narrow a checked surface the operator
 *  should think about in the file, not flip from a dashboard. */
function DecisionEditor({ guardrail, settings }: { guardrail: SettingsGuardrailSummary; settings: AsyncState<SettingsView> }) {
  const [draft, setDraft] = useState({
    provider: '', mode: '', on_error: '', shadow: false,
    llm_base_url: '', llm_model: '', llm_api_key: '',
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [apply, setApply] = useState<{ tone: 'success' | 'warning' | 'info'; text: string } | null>(null)
  const baseline = useRef({ provider: '', mode: '', on_error: '', shadow: false, llm_base_url: '', llm_model: '' })
  const lock = useRef(false)
  useEffect(() => {
    const next = {
      provider: guardrail.provider || 'none', mode: guardrail.mode || 'auto',
      on_error: guardrail.on_error || '', shadow: guardrail.shadow ?? false,
      llm_base_url: guardrail.llm?.base_url || '', llm_model: guardrail.llm?.model || '',
    }
    const old = baseline.current
    baseline.current = next
    setDraft(current => (current.provider === old.provider && current.mode === old.mode
      && current.on_error === old.on_error && current.shadow === old.shadow
      && current.llm_base_url === old.llm_base_url && current.llm_model === old.llm_model
      ? { ...current, ...next, llm_api_key: '' } : current))
  }, [guardrail])
  const dirty = draft.provider !== baseline.current.provider || draft.mode !== baseline.current.mode
    || draft.on_error !== baseline.current.on_error || draft.shadow !== baseline.current.shadow
    || draft.llm_base_url !== baseline.current.llm_base_url || draft.llm_model !== baseline.current.llm_model
    || draft.llm_api_key.trim() !== ''

  async function save() {
    if (lock.current || !settings.data || !dirty) return
    lock.current = true; setBusy(true); setError(''); setApply(null)
    try {
      const llm: Record<string, unknown> = {
        base_url: draft.llm_base_url.trim(),
        model: draft.llm_model.trim(),
      }
      if (draft.llm_api_key.trim()) llm.api_key = draft.llm_api_key.trim()
      const next = await api.saveSettings({
        ...settingsModelPayload(settings.data),
        guardrail_decision_write: {
          provider: draft.provider, mode: draft.mode,
          ...(draft.on_error ? { on_error: draft.on_error } : {}),
          shadow: draft.shadow,
          llm,
        },
      })
      settings.setData(next)
      const g = next.guardrail
      baseline.current = {
        provider: g?.provider || 'none', mode: g?.mode || 'auto', on_error: g?.on_error || '',
        shadow: g?.shadow ?? false, llm_base_url: g?.llm?.base_url || '', llm_model: g?.llm?.model || '',
      }
      setDraft({ ...baseline.current, llm_api_key: '' })
      setApply(settingsApplyNotice(next))
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) } finally { lock.current = false; setBusy(false) }
  }

  const usesLLMJudge = draft.provider === 'llm' || draft.provider === 'hybrid'
  return (
    <div className="space-y-2.5 border-t border-hairline pt-2.5" data-testid="guardrail-decision">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[12px] font-medium text-ink-2">裁判决策</span>
        <span className="text-[11px] text-ink-3">guardrail · scan 启动时读取；scope 与 allow_prefixes 仍在 cyber.yaml 手改</span>
      </div>
      <div className="grid gap-2 sm:grid-cols-3">
        <Field label="裁判来源（provider）">
          <select
            value={draft.provider}
            onChange={e => { setDraft(d => ({ ...d, provider: e.target.value })); setError('') }}
            disabled={busy}
            aria-label="守门裁判来源"
            className="h-8 w-full rounded-md border border-input bg-surface-2/50 px-2 text-[12px] text-ink hover:border-ink-3/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <option value="none">none — 不判定（默认）</option>
            <option value="llm">llm — OpenAI 兼容模型（无需 JEV）</option>
            <option value="jev">jev — JEV 两阶段判定</option>
            <option value="hybrid">hybrid — 小模型先判，异常交 JEV</option>
          </select>
        </Field>
        <Field label="裁决模式（mode）">
          <select
            value={draft.mode}
            onChange={e => { setDraft(d => ({ ...d, mode: e.target.value })); setError('') }}
            disabled={busy}
            aria-label="守门裁决模式"
            className="h-8 w-full rounded-md border border-input bg-surface-2/50 px-2 text-[12px] text-ink hover:border-ink-3/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <option value="auto">auto — 机器裁决直接生效</option>
            <option value="safe">safe — 非放行转人工审批</option>
          </select>
        </Field>
        <Field label="裁判失败时（on_error）" hint={draft.provider === 'llm' || draft.provider === 'hybrid' ? 'llm/hybrid 专用' : 'jev 自带默认'}>
          <select
            value={draft.on_error}
            onChange={e => { setDraft(d => ({ ...d, on_error: e.target.value })); setError('') }}
            disabled={busy}
            aria-label="守门裁判失败处理"
            className="h-8 w-full rounded-md border border-input bg-surface-2/50 px-2 text-[12px] text-ink hover:border-ink-3/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <option value="">review — 转人工复核（默认）</option>
            <option value="block">block — 直接拦截</option>
            <option value="allow">allow — 放行（fail-open）</option>
          </select>
        </Field>
      </div>
      <label className="flex items-center gap-2 text-[12px] text-ink-2">
        <input
          type="checkbox"
          checked={draft.shadow}
          disabled={busy}
          onChange={e => { setDraft(d => ({ ...d, shadow: e.target.checked })); setError('') }}
          aria-label="影子模式（shadow）"
          className="h-3.5 w-3.5 accent-current"
        />
        影子模式（shadow）——只判定并记录，永远放行；用于灰度统计
      </label>

      {usesLLMJudge && (
        <div className="space-y-2 rounded-lg bg-surface-2 p-2.5">
          <p className="text-[11px] leading-relaxed text-ink-3">
            provider={draft.provider} 走 OpenAI 兼容的 <code className="rounded bg-surface-2 px-1 font-mono text-[11px]">/chat/completions</code>
            ：任何模型都能当裁判，不需要 JEV。判定提示词刻意偏向放行——只有明确且不可逆的破坏才判 BLOCK，不确定一律放行并记录。
          </p>
          <div className="grid gap-2 sm:grid-cols-2">
            <Field label="裁判 Base URL">
              <Input
                value={draft.llm_base_url}
                onChange={e => { setDraft(d => ({ ...d, llm_base_url: e.target.value })); setError('') }}
                disabled={busy}
                aria-label="LLM 裁判 Base URL"
                spellCheck={false}
                autoCapitalize="none"
                placeholder="https://relay.example/v1"
                className="font-mono text-[12px]"
              />
            </Field>
            <Field label="裁判模型">
              <Input
                value={draft.llm_model}
                onChange={e => { setDraft(d => ({ ...d, llm_model: e.target.value })); setError('') }}
                disabled={busy}
                aria-label="LLM 裁判模型"
                spellCheck={false}
                autoCapitalize="none"
                className="font-mono text-[12px]"
              />
            </Field>
          </div>
          <Field
            label="裁判 API Key"
            hint={guardrail.llm?.api_key_set ? '已配置——留空保持不变，输入新值即轮换' : '未配置。本地网关可不填'}
          >
            <Input
              type="password"
              value={draft.llm_api_key}
              onChange={e => { setDraft(d => ({ ...d, llm_api_key: e.target.value })); setError('') }}
              disabled={busy}
              aria-label="LLM 裁判 API Key"
              spellCheck={false}
              autoCapitalize="none"
              autoComplete="new-password"
              placeholder={guardrail.llm?.api_key_set ? '············（留空保持不变）' : '粘贴 API Key'}
              className="font-mono text-[12px]"
            />
          </Field>
        </div>
      )}

      {error && <p className="text-[12px] text-destructive" role="alert">{error}</p>}
      {apply && <Callout tone={apply.tone}>{apply.text}</Callout>}

      <div className="flex items-center gap-2">
        <Button
          type="button"
          size="sm"
          disabled={busy || !dirty}
          aria-label="保存守门决策"
          onClick={() => { void save() }}
        >
          {busy ? '保存中…' : '保存决策'}
        </Button>
      </div>
    </div>
  )
}

function JEVEditor({ jev, settings }: { jev?: SettingsJEVSummary; settings: AsyncState<SettingsView> }) {
  const [draft, setDraft] = useState({ base_url: '', model: '', api_key: '' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [apply, setApply] = useState<{ tone: 'success' | 'warning' | 'info'; text: string } | null>(null)
  const baseline = useRef({ base_url: '', model: '' })
  const lock = useRef(false)
  useEffect(() => {
    const next = { base_url: jev?.base_url || '', model: jev?.model || '' }
    const old = baseline.current
    baseline.current = next
    setDraft(current => (current.base_url === old.base_url && current.model === old.model ? { ...current, ...next } : current))
  }, [jev])
  const configured = jev?.configured ?? false
  const dirty = draft.base_url.trim() !== baseline.current.base_url
    || draft.model.trim() !== baseline.current.model
    || draft.api_key.trim() !== ''

  async function save(clear?: boolean) {
    if (lock.current || !settings.data) return
    if (!dirty && !clear) return
    lock.current = true; setBusy(true); setError(''); setApply(null)
    try {
      const write: Record<string, unknown> = {
        base_url: draft.base_url.trim(),
        model: draft.model.trim(),
      }
      if (clear) write.clear_api_key = true
      else if (draft.api_key.trim()) write.api_key = draft.api_key.trim()
      // A PUT applies the whole settings view, so the model half rides along
      // (same discipline as the proxy and capture cards).
      const next = await api.saveSettings({ ...settingsModelPayload(settings.data), jev_write: write })
      settings.setData(next)
      baseline.current = { base_url: next.jev?.base_url || '', model: next.jev?.model || '' }
      setDraft({ base_url: baseline.current.base_url, model: baseline.current.model, api_key: '' })
      setApply(settingsApplyNotice(next))
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) } finally { lock.current = false; setBusy(false) }
  }

  // An older server has no jev projection: render nothing rather than an
  // editor whose save would be rejected.
  if (!jev) return null
  return (
    <div className="space-y-2.5 border-t border-hairline pt-2.5" data-testid="guardrail-jev">
      <div className="flex flex-wrap items-center gap-2">
        {configured ? (
          <KeyRound className="h-4 w-4 text-success" aria-hidden="true" />
        ) : (
          <KeyRound className="h-4 w-4 text-ink-3" aria-hidden="true" />
        )}
        <Badge size="sm" variant={configured ? 'success' : 'muted'}>
          {configured ? 'JEV 凭据已配置' : 'JEV 凭据未配置（llm 模式不需要）'}
        </Badge>
        <span className="text-[11px] text-ink-3">extensions.jev · scan 启动时读取</span>
      </div>

      {jev.reason && (
        <Callout tone="warning" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
          {jev.reason}
        </Callout>
      )}

      <div className="grid gap-2 sm:grid-cols-2">
        <Field label="Base URL" hint="留空 = 引擎默认端点">
          <Input
            value={draft.base_url}
            onChange={e => { setDraft(d => ({ ...d, base_url: e.target.value })); setError('') }}
            disabled={busy}
            aria-label="JEV Base URL"
            spellCheck={false}
            autoCapitalize="none"
            placeholder="https://relay.example/v1"
            className="font-mono text-[12px]"
          />
        </Field>
        <Field label="模型" hint="留空 = 引擎默认">
          <Input
            value={draft.model}
            onChange={e => { setDraft(d => ({ ...d, model: e.target.value })); setError('') }}
            disabled={busy}
            aria-label="JEV 模型"
            spellCheck={false}
            autoCapitalize="none"
            className="font-mono text-[12px]"
          />
        </Field>
      </div>
      <Field
        label="API Key"
        hint={configured ? '已配置——留空保持不变，输入新值即轮换' : '未配置。也可用 TYPESAFE_API_KEY 环境变量'}
      >
        <Input
          type="password"
          value={draft.api_key}
          onChange={e => { setDraft(d => ({ ...d, api_key: e.target.value })); setError('') }}
          disabled={busy}
          aria-label="JEV API Key"
          spellCheck={false}
          autoCapitalize="none"
          autoComplete="new-password"
          placeholder={configured ? '············（留空保持不变）' : '粘贴 TypeSafe API Key'}
          className="font-mono text-[12px]"
        />
      </Field>

      {jev.source && <p className="text-[11px] text-ink-3">{jev.source}</p>}
      {error && <p className="text-[12px] text-destructive" role="alert">{error}</p>}
      {apply && <Callout tone={apply.tone}>{apply.text}</Callout>}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          size="sm"
          disabled={busy || !dirty}
          aria-label="保存 JEV 凭据"
          onClick={() => { void save() }}
        >
          {busy ? '保存中…' : '保存凭据'}
        </Button>
        {configured && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={busy}
            aria-label="清除 JEV 密钥"
            onClick={() => { void save(true) }}
          >
            清除密钥
          </Button>
        )}
      </div>
    </div>
  )
}
