// The node detail cards (docs/14 §7.3): one expanded row renders four quiet
// cards — 身份 / 运行 / 资源 / 健康 — from rows the lib assembles. Purely
// presentational: the same component serves remote rows (heartbeat data) and
// the local row (the control plane's own sample).
import { cn } from '@cyber/theme'
import type { CardRow, HealthTone } from '@/lib/remote-nodes'

export interface NodeCardsData {
  identity: CardRow[]
  runtime: CardRow[]
  resources: CardRow[]
  health: { state: 'healthy' | 'degraded' | 'abnormal' | ''; reasons: string[] }
  tone: HealthTone
  label: string
}

function Card({ title, rows, tone, extra }: { title: string; rows: CardRow[]; tone?: HealthTone; extra?: string }) {
  return (
    <div className="min-w-0 rounded-lg bg-surface-2/60 p-3" role="group" aria-label={title}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[11px] font-medium text-ink-2">{title}</span>
        {extra && (
          <span
            className={cn(
              'text-[11px]',
              tone === 'success' && 'text-success',
              tone === 'warning' && 'text-warning',
              tone === 'destructive' && 'text-destructive',
              (!tone || tone === 'muted') && 'text-ink-3',
            )}
          >
            {extra}
          </span>
        )}
      </div>
      <dl className="mt-2 space-y-1">
        {rows.map((r) => (
          <div key={r.label} className="flex items-baseline gap-2 text-[11px] leading-4">
            <dt className="w-16 shrink-0 text-ink-3">{r.label}</dt>
            <dd className="zrcin-break min-w-0 flex-1 font-mono text-ink-2">{r.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

export function NodeCards({ data, ariaLabel }: { data: NodeCardsData; ariaLabel: string }) {
  const healthRows: CardRow[] =
    data.health.state === ''
      ? [{ label: '状态', value: '暂无采样，等待首次心跳' }]
      : [{ label: '状态', value: data.label }]
  return (
    <div
      className="mt-2 grid grid-cols-1 gap-2 rounded-lg border border-hairline bg-surface-1/40 p-3 @xl:grid-cols-2 @3xl:grid-cols-4"
      role="region"
      aria-label={ariaLabel}
    >
      <Card title="身份" rows={data.identity} />
      <Card title="运行" rows={data.runtime} />
      <Card title="资源" rows={data.resources} />
      <Card title="健康" rows={healthRows} tone={data.tone} extra={data.label} />
      {data.health.reasons.length > 0 && (
        <ul className="@3xl:col-span-4 space-y-0.5" aria-label="健康原因">
          {data.health.reasons.map((r) => (
            <li key={r} className="zrcin-break text-[11px] leading-4 text-warning">
              · {r}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
