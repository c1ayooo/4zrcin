import { useMemo } from 'react'
import { ChevronRight, Coins } from 'lucide-react'
import { Badge, Button, Callout, Skeleton } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { Panel } from '@/components/Page'
import { fmtInt } from '@/lib/format'
import {
  usageBreakdownView,
  usageComposition,
  usageCoverage,
  usageHourlySeries,
  usageText,
} from '@/lib/usage-breakdown'
import type { UsageBreakdown } from '@/lib/types'

/** One colour per spender. The executor is the work (info), the judge is
 *  overhead (caution); the commander stays neutral so the largest segment does
 *  not read as an alert. */
const SEGMENT_COLOR = {
  commander: 'bg-ink-3/70',
  executor: 'bg-info/70',
  judge: 'bg-caution/70',
} as const

/**
 * Overview token spend: who spent it (composition), when it went out (24h
 * rhythm), and what the numbers do not cover.
 *
 * Two different denominators live here on purpose, and each one is labelled:
 *   - composition is the project's all-in accounting (includes the guardrail
 *     judge, read from run journals);
 *   - the rhythm is the run ledger (commander + executor + support) and never
 *     includes the judge, because judge usage has no run row.
 *
 * Every figure is model-reported. A missing bucket prints "—" and disables
 * shares rather than renormalising the known parts into a fake 100%; an
 * in-progress hour is marked and never eligible for the peak.
 */
export function TokenSpendPanel({
  breakdown,
  error,
  loading,
  runs = [],
  onNavigate,
  now,
}: {
  breakdown?: UsageBreakdown
  /** Fetch error: degraded, not destructive — the last numbers stay. */
  error?: string
  loading?: boolean
  /** Ledger runs; the caller already polls them for the recent-runs table. */
  runs?: readonly { started_at?: string; tokens?: number }[]
  onNavigate?: (view: string) => void
  /** Pinned by tests; production reads the wall clock. */
  now?: number
}) {
  const view = useMemo(() => usageBreakdownView(breakdown), [breakdown])
  const composition = useMemo(() => usageComposition(view), [view])
  const series = useMemo(() => usageHourlySeries(runs, { now }), [runs, now])
  const coverage = useMemo(() => usageCoverage(breakdown), [breakdown])

  const title = (
    <span className="flex items-center gap-1.5">
      <Coins className="h-3.5 w-3.5 text-ink-3" aria-hidden="true" /> Token 消耗
    </span>
  )
  const extra = (
    <>
      <Badge size="sm" variant="outline">只读 · 模型上报</Badge>
      {onNavigate && (
        <Button size="xs" variant="ghost" onClick={() => onNavigate('runs')}>
          运行分账 <ChevronRight className="h-3 w-3" aria-hidden="true" />
        </Button>
      )}
    </>
  )

  if (!breakdown && loading) {
    return (
      <Panel data-testid="overview-token-spend" title={title} extra={extra} className="shrink-0">
        <div data-testid="token-spend-loading" className="grid gap-4 md:grid-cols-12" aria-busy="true">
          <div className="space-y-2 md:col-span-5">
            <Skeleton className="h-3 w-32" />
            <Skeleton className="h-2 w-full" />
            <Skeleton className="h-3 w-full" />
            <Skeleton className="h-3 w-4/5" />
          </div>
          <div className="space-y-2 md:col-span-4">
            <Skeleton className="h-3 w-28" />
            <Skeleton className="h-9 w-full" />
          </div>
          <div className="space-y-2 md:col-span-3">
            <Skeleton className="h-3 w-10" />
            <Skeleton className="h-3 w-full" />
            <Skeleton className="h-3 w-3/4" />
          </div>
        </div>
      </Panel>
    )
  }

  const denominator = composition.total
  const segmented =
    composition.shareKnown && denominator !== undefined && denominator > 0
  const compositionLabel = `Token 构成：${composition.segments
    .map((segment) => `${segment.label} ${segment.text}${segment.pctText === '—' ? '（占比未上报）' : `（${segment.pctText}）`}`)
    .join(' · ')}`
  const seriesLabel = `近 24 小时运行账本 token（不含裁判）：合计 ${series.totalText}${
    series.previous !== undefined ? `，前 24 小时 ${usageText(series.previous)}` : ''
  }${series.deltaPct !== undefined ? `，变化 ${series.deltaPct}%` : ''}`
  const barMax = Math.max(1, ...series.buckets.map((bucket) => bucket.tokens ?? 0))

  return (
    <Panel
      data-testid="overview-token-spend"
      title={title}
      extra={extra}
      className="shrink-0"
      bodyClassName="grid gap-4 p-4 md:grid-cols-12"
    >
      {/* Composition: the project's cumulative split. */}
      <section className="min-w-0 md:col-span-5" aria-label="Token 消耗结构">
        <div className="flex items-baseline gap-2">
          <span className="zrcin-eyebrow">消耗结构 · 项目累计</span>
          <span className="ml-auto shrink-0 font-mono text-[12px] zrcin-nums text-ink-2">{composition.totalText}</span>
        </div>
        <div
          data-testid="token-composition-bar"
          role="img"
          aria-label={compositionLabel}
          className="mt-2 flex h-2 overflow-hidden rounded-full bg-surface-3/60"
        >
          {segmented &&
            composition.segments.map((segment) =>
              segment.value && segment.value > 0 ? (
                <div
                  key={segment.key}
                  className={cn('h-full', SEGMENT_COLOR[segment.key])}
                  style={{ width: `${(segment.value / denominator!) * 100}%` }}
                />
              ) : null,
            )}
        </div>
        <ul className="mt-2 space-y-1">
          {composition.segments.map((segment) => (
            <li key={segment.key} className="flex items-center gap-2">
              <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', SEGMENT_COLOR[segment.key])} aria-hidden="true" />
              <span className="min-w-0 truncate text-[11px] text-ink-3">{segment.label}</span>
              <span className="ml-auto shrink-0 font-mono text-[12px] zrcin-nums text-ink-2">{segment.text}</span>
              <span className="w-8 shrink-0 text-right font-mono text-[11px] zrcin-nums text-ink-3">{segment.pctText}</span>
            </li>
          ))}
        </ul>
        {!composition.shareKnown && (
          <p className="mt-1.5 text-[11px] leading-relaxed text-ink-3">
            占比不显示：{composition.unknown.join('、')} 未上报；已知段不归一化。
          </p>
        )}
        {!breakdown && !loading && (
          <p className="mt-1.5 text-[11px] leading-relaxed text-ink-3">
            还没有读到 Token 分账；不估算、不补零。
          </p>
        )}
      </section>

      {/* Rhythm: the run ledger over the last 24 local hours. */}
      <section className="min-w-0 md:col-span-4" aria-label="近 24 小时 token 节奏">
        <div className="flex items-baseline gap-2">
          <span className="zrcin-eyebrow">近 24 小时 · 运行账本</span>
          <span className="ml-auto shrink-0 text-[10px] text-ink-3">不含裁判</span>
        </div>
        <div className="mt-1 flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="font-mono text-[16px] font-semibold zrcin-nums text-ink">{series.totalText}</span>
          <span className="text-[11px] text-ink-3">
            前 24h <span className="ml-0.5 font-mono zrcin-nums text-ink-2">{usageText(series.previous)}</span>
          </span>
          {series.deltaPct !== undefined && (
            <span
              className={cn(
                'font-mono text-[11px] zrcin-nums',
                series.deltaPct > 0 ? 'text-caution' : 'text-ink-3',
              )}
              title="当前窗口含进行中的小时，对比略偏低"
            >
              {series.deltaPct > 0 ? '+' : ''}
              {series.deltaPct}%
            </span>
          )}
        </div>
        <div
          data-testid="token-hourly-bars"
          role="img"
          aria-label={seriesLabel}
          className="mt-2 flex h-9 items-end gap-[2px]"
        >
          {series.buckets.map((bucket) => {
            const height =
              bucket.tokens === undefined || bucket.tokens <= 0
                ? 2
                : Math.max(3, Math.round((bucket.tokens / barMax) * 100))
            return (
              <span
                key={bucket.key}
                title={`${bucket.label} · ${
                  bucket.tokens === undefined ? '未上报' : fmtInt(bucket.tokens)
                }${bucket.partial ? '（进行中）' : ''}`}
                className={cn(
                  'flex-1 rounded-t-[1px]',
                  bucket.tokens === undefined
                    ? 'bg-ink-3/25'
                    : bucket.tokens <= 0
                      ? 'bg-surface-3'
                      : bucket.partial
                        ? 'bg-info/40'
                        : 'bg-info/70',
                )}
                style={{ height: height === 2 ? '2px' : `${height}%` }}
              />
            )
          })}
        </div>
        <div className="mt-1 flex justify-between text-[10px] text-ink-3">
          <span>24h 前</span>
          <span>12h 前</span>
          <span>现在</span>
        </div>
        <div className="mt-1 space-y-0.5 text-[11px] leading-4 text-ink-3">
          {series.peak && (
            <div>
              峰值 <span className="font-mono zrcin-nums text-ink-2">{series.peak.label}</span> ·{' '}
              <span className="font-mono zrcin-nums text-ink-2">{fmtInt(series.peak.tokens)}</span>
            </div>
          )}
          {series.unknownRuns > 0 && (
            <div>{fmtInt(series.unknownRuns)} 个 run 未上报，柱高只含已上报用量</div>
          )}
        </div>
      </section>

      {/* Coverage: what the numbers do not include, and where they come from. */}
      <section className="min-w-0 md:col-span-3" aria-label="Token 上报覆盖">
        <div className="zrcin-eyebrow">覆盖 · 项目累计</div>
        <div className="mt-1.5 space-y-1 text-[11px] leading-4 text-ink-3">
          {breakdown ? (
            <>
              {coverage.unknownRuns > 0 && (
                <div>{`${fmtInt(coverage.unknownRuns)} 个 run 未上报用量`}</div>
              )}
              {coverage.missingCalls > 0 && (
                <div>{`${fmtInt(coverage.missingCalls)} 次调用缺用量`}</div>
              )}
              {coverage.unknownRuns === 0 && coverage.missingCalls === 0 && <div>所有 run 都上报了用量</div>}
              <div>裁判来自 run journal</div>
            </>
          ) : (
            <div>{loading ? '读取中…' : '分账尚未读取'}</div>
          )}
          {/* Backend diagnostics are reference: one summary line, the full
              reasons one click away. A wall of them would outweigh the numbers
              this card exists for, and they repeat the counts above. */}
          {coverage.unavailable.length > 0 && (
            <details>
              <summary className="cursor-pointer list-none text-[11px] text-ink-3 transition-colors hover:text-ink-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
                缺口说明 {coverage.unavailable.length} 条
              </summary>
              <ul className="mt-1 space-y-1">
                {coverage.unavailable.map((reason) => (
                  <li key={reason} className="zrcin-break text-[11px] leading-4 text-ink-3/80">
                    {reason}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </div>
      </section>

      {error && (
        // A not-yet-deployed endpoint is an availability fact, not an alert:
        // keep the rows and say why a number is missing.
        <Callout tone="info" className="md:col-span-12">
          <span className="text-[11px] leading-relaxed">
            Token 分账暂不可用（{error}）。保留 — 与上次成功结果，不显示估算值。
          </span>
        </Callout>
      )}
    </Panel>
  )
}
