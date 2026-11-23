import { useState } from 'react'
import { Activity, CircleAlert, Flag, KeyRound, ShieldCheck } from 'lucide-react'

import { Button } from '@cyber/ui'
import { api } from '@/lib/api'
import { GuardrailCard } from '@/components/GuardrailCard'
import type { OperatorInbox } from '@/lib/types'

/**
 * One operator inbox: authorization requests, per-intent handoffs, stale goal
 * receipts and run warnings, grouped by what the operator has to do. The
 * authorization rows are structured control state, not state.md prose: the
 * control plane creates the generic fallback, and the commander can enrich it
 * on demand.
 */
export function OperatorInboxCard({
  inbox,
  onNavigate,
  onChanged,
  onLocateGuardrail,
  onOpenIntent,
  onOpenRun,
  onOpenAuthorization,
  onOpenGoal,
}: {
  inbox: OperatorInbox
  onNavigate: (view: string) => void
  onChanged: () => void
  /** Point a pending tool approval at the raw call in a run's event stream. */
  onLocateGuardrail?: (target: { callID: string; sessionID: string }) => void
  /** Record-level link: the direction behind a handoff (`?intent=`). */
  onOpenIntent?: (intentID: string) => void
  /** Record-level link: the run behind a warning (`?run=`). */
  onOpenRun?: (runID: string) => void
  /** Record-level link: one authorization request (`?view=access&id=`). */
  onOpenAuthorization?: (requestID: string) => void
  /** Record-level link: one goal's receipt (`?view=state&goal=`). */
  onOpenGoal?: (goalID: string) => void
}) {
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  // The overview's first screen has to keep 目标进度 and 阻塞原因 in view, so the
  // card lists three rows and expands in place: there is no separate 待办 page,
  // and every row keeps its own record link.
  const [showAll, setShowAll] = useState(false)

  async function requestDetails(id: string) {
    setBusy(`details:${id}`)
    setNotice('')
    try {
      await api.requestOperatorDetails(id)
      setNotice('已请求指挥官补齐明细；它会在下一轮写入，不会自动执行动作。')
      onChanged()
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  async function resolve(id: string) {
    setBusy(`resolve:${id}`)
    setNotice('')
    try {
      await api.resolveOperatorRequest(id, '操作员已处理')
      setNotice('请求已标记为已处理。')
      onChanged()
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  // Tool approval always shows its state — including "never checked" and "not
  // enabled" — so the operator learns the guardrail status without a failure.
  // Every other section only exists when it has rows.
  const actionable = inbox.total > 0
  const MAX_ROWS = 3
  const totalRows = inbox.authorization.length + inbox.handoff.length + inbox.acceptance.length + inbox.warnings.length
  const budget = showAll ? Number.POSITIVE_INFINITY : MAX_ROWS
  const authorizationRows = inbox.authorization.slice(0, budget)
  const handoffRows = inbox.handoff.slice(0, Math.max(0, budget - authorizationRows.length))
  const acceptanceRows = inbox.acceptance.slice(0, Math.max(0, budget - authorizationRows.length - handoffRows.length))
  const warningRows = inbox.warnings.slice(
    0,
    Math.max(0, budget - authorizationRows.length - handoffRows.length - acceptanceRows.length),
  )
  const hiddenRows =
    totalRows - authorizationRows.length - handoffRows.length - acceptanceRows.length - warningRows.length
  if (!actionable && !inbox.guardrail) return null

  return (
    <section
      className={
        actionable
          ? 'rounded-xl border border-warning/25 bg-warning/[0.06] px-4 py-3'
          : 'rounded-xl border border-hairline bg-surface-2/30 px-4 py-3'
      }
      aria-label="待操作员处理"
      data-testid="operator-inbox"
    >
      <div className="flex items-start gap-3">
        <span
          className={
            actionable
              ? 'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-warning/25 bg-warning/10 text-warning'
              : 'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-hairline bg-surface-2 text-ink-3'
          }
        >
          {actionable ? <CircleAlert className="h-4 w-4" /> : <ShieldCheck className="h-4 w-4" />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <h2 className="text-[13px] font-semibold text-ink">{actionable ? '待你处理' : '操作员收件箱'}</h2>
            {/* Same number as the rail badge: an exact total only when every
                category answered; otherwise the rows below are a lower bound. */}
            {actionable && (
              <span
                className="font-mono tw-nums text-[11px] text-ink-3"
                title={inbox.summary && !inbox.summary.countable ? '有类别没读到：这是已知的下限，不是精确总数' : undefined}
              >
                {inbox.summary && !inbox.summary.countable ? `${inbox.summary.total}+` : inbox.total}
              </span>
            )}
            {!actionable && <span className="text-[11px] text-ink-3">当前没有需要你决定的请求</span>}
          </div>

          {inbox.guardrail && (
            <div className="mt-2.5 rounded-lg border border-hairline bg-card/60 px-3 py-2">
              <GuardrailCard summary={inbox.guardrail} onChanged={onChanged} onLocateCall={onLocateGuardrail} />
            </div>
          )}

          {authorizationRows.length > 0 && (
            <div className="mt-2.5">
              <div className="flex items-center gap-1.5 text-[12px] font-medium text-ink">
                <KeyRound className="h-3.5 w-3.5 text-warning" /> 需要你授权
              </div>
              <div className="mt-1 space-y-2">
                {authorizationRows.map((req) => (
                  <div key={req.id} className="rounded-lg border border-warning/20 bg-card/70 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <button
                        type="button"
                        className="tw-break text-left text-[12.5px] font-medium text-ink transition-colors hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                        onClick={() => onOpenAuthorization?.(req.id)}
                        disabled={!onOpenAuthorization}
                        title={onOpenAuthorization ? `在授权页打开请求 ${req.id}` : undefined}
                      >
                        {req.action || '目标未达成，没有在办意图'}
                      </button>
                      {req.generic && (
                        <span className="rounded border border-hairline px-1.5 py-0.5 text-[10px] text-ink-3">通用请求</span>
                      )}
                    </div>
                    {(req.asset || req.impact) && (
                      <div className="tw-break mt-0.5 text-[11.5px] leading-relaxed text-ink-2">
                        {req.asset && <span>资产：{req.asset}</span>}
                        {req.asset && req.impact && <span className="px-1 text-ink-3">·</span>}
                        {req.impact && <span>影响：{req.impact}</span>}
                      </div>
                    )}
                    {req.authorization && (
                      <div className="tw-break mt-0.5 text-[11.5px] leading-relaxed text-ink-2">
                        需要授权：{req.authorization}
                      </div>
                    )}
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                      {/* Only the generic fallback needs the commander to fill
                          details. A request the commander already made concrete
                          (POST /api/operator-requests) would only be overwritten. */}
                      {req.generic && (
                        <Button
                          size="xs"
                          variant="outline"
                          disabled={busy !== '' || req.detail_requested}
                          onClick={() => void requestDetails(req.id)}
                        >
                          {req.detail_requested ? '已请求明细' : busy === `details:${req.id}` ? '请求中…' : '让指挥官列明细'}
                        </Button>
                      )}
                      <Button
                        size="xs"
                        variant="ghost"
                        disabled={busy !== ''}
                        onClick={() => void resolve(req.id)}
                      >
                        {busy === `resolve:${req.id}` ? '处理中…' : '标记已处理'}
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {handoffRows.length > 0 && (
            <div className="mt-2.5">
              <div className="flex items-center gap-1.5 text-[12px] font-medium text-ink">
                <Flag className="h-3.5 w-3.5 text-warning" /> 等你操作
              </div>
              <div className="mt-1 space-y-1">
                {handoffRows.map((item) => (
                  <button
                    key={item.request.id}
                    type="button"
                    className="block w-full rounded-lg border border-hairline bg-card/60 px-3 py-1.5 text-left transition-colors hover:border-warning/30"
                    onClick={() =>
                      onOpenIntent
                        ? onOpenIntent(item.intent_id)
                        : onNavigate('access')
                    }
                    title={item.request.request}
                  >
                    <span className="line-clamp-1 block text-[12px] font-medium text-ink" title={item.title}>
                      {item.title}
                    </span>
                    <span className="line-clamp-2 block text-[11.5px] leading-relaxed text-ink-2">
                      {item.request.request}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {(acceptanceRows.length > 0 || warningRows.length > 0) && (
            <div className="mt-2.5">
              <div className="flex items-center gap-1.5 text-[12px] font-medium text-ink">
                <Activity className="h-3.5 w-3.5 text-warning" /> 需要你验收 / 证据告警
              </div>
              <div className="mt-1 space-y-1">
                {acceptanceRows.map((item) => (
                  <button
                    key={item.goal_id}
                    type="button"
                    className="block w-full rounded-lg border border-hairline bg-card/60 px-3 py-1.5 text-left transition-colors hover:border-warning/30"
                    onClick={() => (onOpenGoal ? onOpenGoal(item.goal_id) : onNavigate('graph'))}
                    title={item.statement}
                  >
                    <span className="line-clamp-1 block text-[12px] font-medium text-ink">目标完成依据已变化</span>
                    <span className="line-clamp-2 block text-[11.5px] leading-relaxed text-ink-2">{item.statement}</span>
                  </button>
                ))}
                {warningRows.map((item) => (
                  <button
                    key={item.run}
                    type="button"
                    className="block w-full rounded-lg border border-hairline bg-card/60 px-3 py-1.5 text-left transition-colors hover:border-warning/30"
                    onClick={() => (onOpenRun ? onOpenRun(item.run) : onNavigate('runs'))}
                    title={item.warnings[0]}
                  >
                    <span className="line-clamp-1 block text-[12px] font-medium text-ink">
                      运行 {item.run} 有交接告警
                    </span>
                    <span className="line-clamp-2 block text-[11.5px] leading-relaxed text-ink-2">
                      {item.warnings[0]}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {hiddenRows > 0 && (
            <div className="mt-2.5">
              <Button size="xs" variant="outline" onClick={() => setShowAll(true)}>
                还有 <span className="font-mono tw-nums">{hiddenRows}</span> 条待处理 · 展开全部
              </Button>
            </div>
          )}
          {showAll && totalRows > MAX_ROWS && (
            <div className="mt-2">
              <Button size="xs" variant="ghost" onClick={() => setShowAll(false)}>收起</Button>
            </div>
          )}

          {notice && (            <div className="mt-2 text-[11px] leading-relaxed text-ink-3" role="status">
              {notice}
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
