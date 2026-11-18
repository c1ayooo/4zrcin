import { ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react'
import { Badge, Callout } from '@cyber/ui'
import { Panel } from '@/components/Page'
import {
  guardrailFailOpenText,
  guardrailLLMLabel,
  guardrailPolicyFlags,
  guardrailProviderLabel,
  guardrailScopeLabel,
} from '@/lib/settings'
import type { SettingsGuardrailSummary } from '@/lib/types'

/**
 * Read-only summary of the operator's `extensions.guardrail` policy.
 *
 * The settings page does not edit the judge (the operator writes the section in
 * cyber.yaml directly), so this card only reports what is in effect:
 *
 *  - provider none / configured=false is "守门未启用" in neutral grey — a
 *    configuration state, not an error;
 *  - on_error=allow is the explicit fail-open tradeoff and must be called out
 *    prominently: a let-through is not a passed judgement (docs §10.3);
 *  - shadow=true is "只判定不拦截" — grey rollout, never enforcement;
 *  - JEV/LLM show only whether they are configured (plus model/base_url), never
 *    a key.
 */
export function GuardrailPolicyCard({ guardrail }: { guardrail?: SettingsGuardrailSummary }) {
  // An older server has no guardrail projection at all: render nothing instead
  // of inventing a "not configured" state that could be wrong.
  if (!guardrail) return null
  const flags = guardrailPolicyFlags(guardrail)
  const allowPrefixes = guardrail.allow_prefixes_count ?? 0
  return (
    <Panel
      title="守门策略"
      extra={<Badge variant="outline" size="sm">只读 · cyber.yaml extensions.guardrail</Badge>}
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
          守门未启用：工具调用前不会经过自动判定，也不会进入人工审批流程。要在<code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">extensions.guardrail</code>
          配置 <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">provider</code>（jev / llm / hybrid）后重启宿主才会启用。
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
          <dd className="tw-break min-w-0 text-ink-2">{guardrailProviderLabel(guardrail.provider)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">模式</dt>
          <dd className="tw-break min-w-0 text-ink-2">{guardrail.mode?.trim() || '默认'}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">生效工具</dt>
          <dd className="tw-break min-w-0 text-ink-2">{guardrailScopeLabel(guardrail.scope)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">异常处理</dt>
          <dd className={`tw-break min-w-0 ${flags.failOpen ? 'font-medium text-warning' : 'text-ink-2'}`}>
            {guardrail.on_error?.trim() || '未设置（默认 review）'}
            {flags.failOpen ? ' · 自动放行' : guardrail.on_error === 'review' ? ' · 转人工复核' : guardrail.on_error === 'block' ? ' · 直接拦截' : ''}
          </dd>
        </div>
        {allowPrefixes > 0 && (
          <div className="flex min-w-0 items-baseline gap-1.5">
            <dt className="shrink-0 text-ink-3">前缀放行</dt>
            <dd className="tw-break min-w-0 text-ink-2">mode=safe 下按前缀直接放行 {allowPrefixes} 条</dd>
          </div>
        )}
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">JEV 凭据</dt>
          <dd className={`tw-break min-w-0 ${guardrail.jev_configured ? 'text-ink-2' : 'text-ink-3'}`}>
            {guardrail.jev_configured ? '已配置' : '未配置'}
          </dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">小模型裁判</dt>
          <dd className="tw-break min-w-0 font-mono text-[11px] text-ink-2">{guardrailLLMLabel(guardrail.llm)}</dd>
        </div>
      </dl>

      {flags.failOpen && (
        <p className="text-[11px] leading-relaxed text-ink-3">{guardrailFailOpenText}</p>
      )}
    </Panel>
  )
}
