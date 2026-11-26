// The node detail cards (docs/14 §7.3): one expanded row renders four quiet
// cards — 身份 / 运行 / 资源 / 健康 — from rows the lib assembles. Purely
// presentational: the same component serves remote rows (heartbeat data) and
// the local row (the control plane's own sample).
import { cn } from '@cyber/theme'
import type { CardRow, HealthTone, ResourceRings } from '@/lib/remote-nodes'

export interface NodeCardsData {
  identity: CardRow[]
  runtime: CardRow[]
  /** Percentage rings for the resource card (CPU / memory / disk). */
  rings: ResourceRings
  /** Kept for the detail rows under the rings (scan memory line). */
  resources: CardRow[]
  health: { state: 'healthy' | 'degraded' | 'abnormal' | ''; reasons: string[] }
  tone: HealthTone
  label: string
}

/** usageTone picks the ring color from the busy fraction (0-1). */
function usageTone(frac: number): 'ink' | 'warning' | 'destructive' {
  if (frac > 0.85) return 'destructive'
  if (frac > 0.6) return 'warning'
  return 'ink'
}

/** UsageRing renders one percentage circle: the ring is the fraction busy,
 * the number sits inside. A missing counter renders a grey dash — the kernel
 * could not provide it, and 0% would be a lie. */
function UsageRing({ label, frac, sub }: { label: string; frac: number | undefined; sub?: string }) {
  const size = 56
  const r = 24
  const c = 2 * Math.PI * r
  const known = frac != null && !Number.isNaN(frac)
  const clamped = known ? Math.min(1, Math.max(0, frac)) : 0
  const tone = known ? usageTone(clamped) : 'muted'
  return (
    <div className="flex flex-col items-center gap-1" role="group" aria-label={`${label} 使用率${known ? ` ${Math.round(clamped * 100)}%` : '（不支持）'}`}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" className="stroke-hairline" strokeWidth={5} />
        {known && (
          <circle
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            strokeWidth={5}
            strokeLinecap="round"
            strokeDasharray={c}
            strokeDashoffset={c * (1 - clamped)}
            transform={`rotate(-90 ${size / 2} ${size / 2})`}
            className={cn('transition-[stroke-dashoffset]', tone === 'ink' && 'stroke-ink-2', tone === 'warning' && 'stroke-warning', tone === 'destructive' && 'stroke-destructive')}
          />
        )}
        <text x="50%" y="54%" textAnchor="middle" dominantBaseline="middle" className={cn('font-mono text-[11px]', known ? 'fill-ink' : 'fill-ink-3')}>
          {known ? `${Math.round(clamped * 100)}%` : '—'}
        </text>
      </svg>
      <span className="text-[10px] leading-3 text-ink-3">{label}</span>
      {sub && <span className="font-mono text-[10px] leading-3 text-ink-3">{sub}</span>}
    </div>
  )
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
      <div className="min-w-0 rounded-lg bg-surface-2/60 p-3" role="group" aria-label="资源">
        <span className="text-[11px] font-medium text-ink-2">资源</span>
        <div className="mt-2 flex items-start justify-around gap-1">
          <UsageRing label="CPU" frac={data.rings.cpu ?? undefined} />
          <UsageRing label="内存" frac={data.rings.mem ?? undefined} sub={data.rings.memSub} />
          <UsageRing label="磁盘剩余" frac={data.rings.disk ?? undefined} sub={data.rings.diskSub} />
        </div>
        {data.rings.scanRSS && (
          <div className="mt-2 flex items-baseline gap-2 text-[11px] leading-4">
            <span className="text-ink-3">scan 内存</span>
            <span className="font-mono text-ink-2">{data.rings.scanRSS}</span>
          </div>
        )}
      </div>
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
