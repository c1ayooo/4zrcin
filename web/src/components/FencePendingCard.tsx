// The 通信 sidebar's pending-fence card: the out-of-range assets waiting for
// the operator's decision, one row per host with the two buttons the resolve
// endpoint takes. The queue is the server's own (GET /api/fence-pending); this
// card only renders it, so an approval here and an approval through the API
// are the same ledger decision — approve joins the fence and tells the
// commander, reject marks the asset denied.
import { useState } from 'react'
import { Badge, Button, useConfirm } from '@cyber/ui'
import { Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { kindLabel } from '@/lib/assets'
import { usePoll } from '@/lib/hooks'
import type { FencePendingAsset } from '@/lib/types'

export function FencePendingCard({ onChanged }: { onChanged?: () => void }) {
  const pending = usePoll(() => api.fencePending(), 8000)
  const rows = pending.data?.pending ?? []
  // No queue, no card: the sidebar stays quiet until something waits.
  if (pending.data && rows.length === 0) return null
  return (
    <FencePendingList
      rows={rows}
      loading={pending.loading && !pending.data}
      error={pending.error}
      onRetry={pending.reload}
      onChanged={onChanged}
    />
  )
}

/** The presentational half, split so tests can render it without a poll. */
export function FencePendingList({
  rows,
  loading = false,
  error = '',
  onRetry,
  onChanged,
}: {
  rows: FencePendingAsset[]
  loading?: boolean
  error?: string
  onRetry?: () => void
  onChanged?: () => void
}) {
  const confirm = useConfirm()
  const [busy, setBusy] = useState('')
  const [receipt, setReceipt] = useState('')
  const [failure, setFailure] = useState('')

  async function resolve(row: FencePendingAsset, approve: boolean) {
    if (busy) return
    if (!approve) {
      const ok = await confirm({
        title: `拒绝 ${row.key}？`,
        description: '拒绝后资产标记为不在授权范围，停测指令生效；之后对它的请求按围栏外处理，不再进入待审。',
        confirmLabel: '拒绝',
        destructive: true,
      })
      if (!ok) return
    }
    setBusy(row.id)
    setReceipt('')
    setFailure('')
    try {
      const out = await api.resolveFencePending([row.id], approve)
      const res = out.results?.[0]
      if (res?.error) {
        setFailure(res.error)
        return
      }
      setReceipt(approve ? `已批准：${row.key} 加入围栏，测试可恢复。` : `已拒绝：${row.key} 不在授权范围。`)
      onChanged?.()
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <Panel title={`待审资产（${rows.length}）`} bodyClassName="p-3" data-testid="fence-pending-card">
      {loading && <p className="text-[10.5px] leading-4 text-ink-3">读取待审队列…</p>}
      {error && (
        <p role="alert" className="mt-1 flex flex-wrap items-center gap-2 text-[11px] leading-4 text-destructive">
          <span className="min-w-0 break-all">读取失败：{error}</span>
          {onRetry && (
            <Button size="xs" variant="outline" onClick={onRetry}>
              重试
            </Button>
          )}
        </p>
      )}
      {rows.length > 0 && (
        <ul role="list" aria-label="待审资产" className="mt-1 space-y-1.5">
          {rows.map((row) => (
            <li key={row.id} className="rounded-md border border-hairline bg-surface-2/40 px-2 py-1.5">
              <div className="flex items-center gap-1.5">
                <Badge size="sm" variant="muted">
                  {kindLabel(row.kind)}
                </Badge>
                <span className="min-w-0 truncate font-mono text-[11px] text-ink" title={row.key}>
                  {row.key}
                </span>
                <span className="ml-auto flex shrink-0 gap-1">
                  <Button size="xs" variant="outline" disabled={busy === row.id} onClick={() => resolve(row, true)}>
                    批准
                  </Button>
                  <Button size="xs" variant="ghost" className="text-destructive" disabled={busy === row.id} onClick={() => resolve(row, false)}>
                    拒绝
                  </Button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
      {receipt && (
        <p data-fence-receipt className="mt-2 text-[11px] leading-4 text-ink-2">
          {receipt}
        </p>
      )}
      {failure && (
        <p role="alert" className="mt-2 text-[11px] leading-4 text-destructive">
          {failure}
        </p>
      )}
    </Panel>
  )
}
