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
 * Summary of the operator's `extensions.guardrail` policy, plus an editor for
 * its JEV credentials (`extensions.jev`).
 *
 * The policy's shape stays read-only (the operator writes that section in
 * cyber.yaml directly); the credentials are the one writable piece — a key is
 * a secret the console can hold but never echo, and retyping it on every
 * rotation is the friction that leaves judges unconfigured. The card reports
 * what is in effect — and, just as important, how to read it:
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
      extra={<Badge variant="outline" size="sm">策略只读 · JEV 凭据可改 · cyber.yaml</Badge>}
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
        <span className="text-[11px] text-ink-3">控制台只读展示，配置由操作员直接写在 cyber.yaml。</span>
      </div>

      {!flags.enabled && (
        <p className="text-[12px] leading-relaxed text-ink-2">
          守门未启用：工具调用前不会经过自动判定，也不会进入人工审批流程。<code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">provider=none</code>
          或未配置就是默认状态；只配置凭据不会启用策略。需要时可以在
          <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">extensions.guardrail</code>
          配 <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">provider: llm</code>（加 llm 子段，不需要 JEV）或 jev / hybrid，重启宿主后生效。
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
