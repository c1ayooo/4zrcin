import { useState } from 'react'
import { Badge, Button, EmptyState, SkeletonList } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { api } from '@/lib/api'
import { fmtTime } from '@/lib/format'
import { useLoad } from '@/lib/hooks'
import type { AuditRow } from '@/lib/types'

const SOURCE_FILTERS: { id: string; label: string }[] = [
  { id: '', label: '全部' },
  { id: 'builtin', label: '保底审计' },
  { id: 'rule', label: '范围规则' },
]

const ACTION_FILTERS: { id: string; label: string }[] = [
  { id: '', label: '全部动作' },
  { id: 'record', label: '记录' },
  { id: 'review', label: '挂审' },
  { id: 'block', label: '拒绝' },
]

function actionBadgeVariant(action?: string) {
  return action === 'block' ? 'destructive' : action === 'review' ? 'warning' : 'muted'
}

function actionLabel(action?: string) {
  return action === 'block' ? '拒绝' : action === 'review' ? '挂审' : '记录'
}

/**
 * The project's command audit trail (docs/03 §5 审计区): every guardrail
 * decision the runs recorded, newest first. Builtin rows — the execution-side
 * guaranteed high-risk command audit — carry the 保底 badge so the operator
 * can always tell "a project rule judged this" from "the platform judged
 * this, and no setting could have turned it off".
 */
export function AuditTrail({ projectId }: { projectId: string }) {
  const [source, setSource] = useState('')
  const [action, setAction] = useState('')
  const [offset, setOffset] = useState(0)
  const trail = useLoad(
    () => api.projectAudit(projectId, { source: source || undefined, action: action || undefined, offset, limit: 50 }),
    [projectId, source, action, offset],
  )
  const rows = trail.data?.rows ?? []
  const total = trail.data?.total ?? 0

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="审计筛选">
        {SOURCE_FILTERS.map(filter => (
          <Button
            key={filter.id}
            type="button"
            size="sm"
            variant={source === filter.id ? 'default' : 'outline'}
            aria-pressed={source === filter.id}
            data-audit-source={filter.id || 'all'}
            onClick={() => { setSource(filter.id); setOffset(0) }}
          >
            {filter.label}
          </Button>
        ))}
        <span className="mx-1 h-4 w-px bg-hairline" aria-hidden="true" />
        {ACTION_FILTERS.map(filter => (
          <Button
            key={filter.id}
            type="button"
            size="sm"
            variant={action === filter.id ? 'default' : 'outline'}
            aria-pressed={action === filter.id}
            data-audit-action={filter.id || 'all'}
            onClick={() => { setAction(filter.id); setOffset(0) }}
          >
            {filter.label}
          </Button>
        ))}
        <span className="tw-nums ml-auto font-mono text-[11px] text-ink-3">共 {total} 条</span>
      </div>

      {trail.loading && !trail.data && <SkeletonList count={4} />}
      {trail.error && <p className="text-[12px] text-destructive">{trail.error}</p>}

      {!trail.loading && rows.length === 0 && (
        <EmptyState
          compact
          title="暂无审计记录"
          description="执行端对工具调用做出判定后会出现在这里。保底审计始终开启，不依赖任何项目配置。"
        />
      )}

      {rows.length > 0 && (
        <ul role="list" className="divide-y divide-hairline" aria-label="命令审计记录">
          {rows.map((row, index) => (
            <AuditRowItem key={`${row.run_id}|${row.call_id}|${index}`} row={row} />
          ))}
        </ul>
      )}

      {total > 50 && (
        <div className="flex items-center gap-2 text-[11px] text-ink-3" role="group" aria-label="审计分页">
          <Button size="xs" variant="ghost" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>
            上一页
          </Button>
          <Button size="xs" variant="ghost" disabled={offset + 50 >= total} onClick={() => setOffset(offset + 50)}>
            下一页
          </Button>
          <span className="tw-nums font-mono">第 {Math.floor(offset / 50) + 1} 页</span>
        </div>
      )}
    </div>
  )
}

function AuditRowItem({ row }: { row: AuditRow }) {
  return (
    <li className="flex flex-wrap items-baseline gap-2 py-1.5 text-[12px] leading-5">
      <Badge size="sm" variant={actionBadgeVariant(row.action)}>{actionLabel(row.action)}</Badge>
      {row.builtin && (
        <Badge size="sm" variant="info" title="执行端内置高危命令审计：始终开启，不可被项目配置关闭">
          保底
        </Badge>
      )}
      {row.rule_id && !row.builtin && (
        <span className="font-mono text-[11px] text-ink-3" title="命中的项目范围规则">{row.rule_id}</span>
      )}
      <span className="font-medium text-ink">{row.tool || '未知工具'}</span>
      <span className="tw-break min-w-0 flex-1 font-mono text-[11px] text-ink-2">{row.reason || '（无原因说明）'}</span>
      <time className="tw-nums whitespace-nowrap font-mono text-[11px] text-ink-3">{fmtTime(row.at)}</time>
      <span className={cn('whitespace-nowrap font-mono text-[11px]', row.builtin ? 'text-ink-3' : 'text-ink-3')}>
        {row.run_id}
      </span>
    </li>
  )
}
