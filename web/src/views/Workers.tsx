import { useEffect, useMemo, useState } from 'react'
import {
  Power,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
  Users,
} from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  Chip,
  EmptyState,
  Field,
  Input,
  ScrollArea,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SkeletonList,
  Spinner,
  StatusIndicator,
  useConfirm,
} from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { ApiError, api } from '@/lib/api'
import { fmtInt, fmtTime, oneLine, statusKind, statusLabel } from '@/lib/format'
import { useLoad, usePoll, type AsyncState } from '@/lib/hooks'
import type {
  PeerRule,
  WorkerView,
  WorkersResponse,
} from '@/lib/types'

/** Raised statuses first: the roster is read top-down as "who is busy now". */
const STATUS_ORDER: Record<string, number> = {
  running: 0,
  claimed: 1,
  waiting: 2,
  pending: 3,
  parked: 4,
  failed: 5,
  canceled: 6,
  done: 7,
}

function workerRank(w: WorkerView): number {
  return STATUS_ORDER[w.status] ?? 8
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Structured codes the server may attach; shown as a mono badge next to the prose. */
function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null
  const code = error instanceof ApiError ? error.code : undefined
  return (
    <div role="alert" className="flex flex-wrap items-center gap-2 text-[12px] text-destructive">
      <span>{errText(error)}</span>
      {code && (
        <Badge size="sm" variant="danger" className="font-mono">
          {code}
        </Badge>
      )}
    </div>
  )
}

/** Roster facet: one compact status filter above the list. */
type WorkerFacet = 'all' | 'running' | 'waiting' | 'failed' | 'done'

const FACETS: { id: WorkerFacet; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'running', label: '在跑' },
  { id: 'waiting', label: '待办' },
  { id: 'failed', label: '失败' },
  { id: 'done', label: '终态' },
]

function facetOf(w: WorkerView): WorkerFacet {
  if (w.status === 'running' || w.status === 'claimed') return 'running'
  // A cancellation is not a failure: the row badge says 取消, and the roster
  // headline reads 停/终态. Bucket it with the terminal states instead of
  // 失败, and keep parked directions under 待办 (they can still be reopened).
  if (w.status === 'failed') return 'failed'
  if (w.status === 'done' || w.status === 'canceled') return 'done'
  return 'waiting'
}

/**
 * Operator supervision of execution: inspect the roster, stop work and manage
 * model defaults and communication permissions. Workers are projections of
 * intents and their latest runs; they are not a separate staffing model.
 */
export function Workers({
  onOpenGraphIntent,
  onOpenRun,
}: {
  /** Cross-page link (docs/34 §4): a worker's direction on the graph (`s:<id>`).
   *  Optional — without it no 图谱 entry is rendered. */
  onOpenGraphIntent?: (intentID: string) => void
  /** Cross-page link: this worker's latest run on the runs page. Optional. */
  onOpenRun?: (runID: string) => void
}) {
  const workers = usePoll(api.workers, 4000)
  const rows = workers.data?.workers ?? []
  const total = rows.length
  const running = rows.filter((w) => w.running).length

  return (
    <Page
      title="执行者"
      subtitle="查看方向执行情况、停止运行、管理消息授权"
      meta={
        <>
          <span className="tw-nums font-mono text-[11px] text-ink-3">工作者 {fmtInt(total)}</span>
          <span className="tw-nums font-mono text-[11px] text-ink-3">在跑 {fmtInt(running)}</span>
        </>
      }
      scroll={false}
    >
      <div className="grid h-full min-h-0 grid-cols-1 gap-4 overflow-y-auto p-4 sm:p-5 xl:grid-cols-[minmax(0,1fr)_360px] xl:overflow-hidden xl:p-6">
        <WorkerRoster
          workers={workers}
          onOpenGraphIntent={onOpenGraphIntent}
          onOpenRun={onOpenRun}
        />
        <div className="flex min-h-0 flex-col gap-4 xl:overflow-y-auto xl:pr-1">
          <PeerPolicyPanel />
          <p className="rounded-lg border border-hairline bg-surface-2/40 px-3 py-2 text-[11px] leading-4 text-ink-3">
            前提政策（硬前提门槛）已移至{' '}
            <a className="text-ink-2 underline underline-offset-2" href="/g/?view=settings">设置 → 项目设置</a>
            ，与项目运行选项一起保存。
          </p>
        </div>
      </div>
    </Page>
  )
}

function WorkerRoster({
  workers,
  onOpenGraphIntent,
  onOpenRun,
}: {
  workers: AsyncState<WorkersResponse>
  onOpenGraphIntent?: (intentID: string) => void
  onOpenRun?: (runID: string) => void
}) {
  const confirm = useConfirm()
  const [notice, setNotice] = useState('')
  const [fail, setFail] = useState<unknown>(null)
  const [query, setQuery] = useState('')
  const [facet, setFacet] = useState<WorkerFacet>('all')
  const [selected, setSelected] = useState<string | null>(null)
  const rows = (workers.data?.workers ?? []).toSorted(
    (a, b) =>
      workerRank(a) - workerRank(b) ||
      (b.created_at > a.created_at ? 1 : b.created_at < a.created_at ? -1 : 0),
  )
  const running = rows.filter((w) => w.running).length
  const needle = query.trim().toLowerCase()
  const filtered = rows.filter((w) => {
    if (facet !== 'all' && facetOf(w) !== facet) return false
    if (!needle) return true
    return [w.title, w.intent_id, w.participant_id, w.profile, w.resolved_profile, w.model, w.run_id, w.node]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
      .includes(needle)
  })

  async function kill(w: WorkerView) {
    const ok = await confirm({
      title: 'kill 这个工作者？',
      description: `${w.title}\n${w.intent_id}\n\n它会先被置 Hold（调度不会复活），然后取消当前 run；已结束的 worker 不能 kill。`,
      confirmLabel: 'kill',
      destructive: true,
    })
    if (!ok) return
    setFail(null)
    setNotice('')
    try {
      const res = await api.killWorker(w.intent_id)
      setNotice(
        `已 kill ${w.intent_id}${res.canceled ? '（run 已取消）' : '（无在跑 run，已落终态）'}`,
      )
      workers.reload()
    } catch (e) {
      setFail(e)
    }
  }

  const firstLoad = workers.loading && !workers.data

  return (
    <Panel
      title={`工作者（${fmtInt(rows.length)}）`}
      className="flex min-h-0 flex-col"
      bodyClassName="flex min-h-0 flex-1 flex-col p-0"
      extra={
        <>
          <span className="tw-nums font-mono text-[11px] text-ink-3">在跑 {running}</span>
        </>
      }
    >
      {/* One 12px toolbar: search + status facets + refresh, all h-8. */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-2">
        <div className="relative min-w-0 flex-1 basis-[160px]">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-3" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索标题 / 意图 / 执行者 / 模型"
            className="h-8 pl-7 text-[12px]"
            aria-label="过滤工作者"
            type="search"
            inputMode="search"
            enterKeyHint="search"
            autoComplete="off"
            spellCheck={false}
          />
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {FACETS.map((f) => (
            <Chip
              key={f.id}
              active={facet === f.id}
              onClick={() => setFacet(f.id)}
              className="h-8 rounded-md px-2.5 text-[12px]"
            >
              {f.label}
            </Chip>
          ))}
        </div>
        <Button
          size="icon-sm"
          variant="outline"
          className="shrink-0"
          aria-label="刷新工作者名单"
          title="立即刷新（页面每 4 秒也会自动刷新）"
          onClick={() => workers.reload()}
        >
          <RefreshCw className={`h-3.5 w-3.5 ${workers.loading ? 'motion-safe:animate-spin' : ''}`} />
        </Button>
      </div>

      {(workers.error || fail || notice) && (
        <div className="shrink-0 space-y-2 border-b border-hairline p-3">
          {workers.error && (
            <Callout tone="destructive">
              <div className="flex flex-wrap items-center gap-2">
                <span>读工作者列表失败：{workers.error}</span>
                <Button size="xs" variant="outline" onClick={() => workers.reload()}>重试</Button>
              </div>
            </Callout>
          )}
          {notice && <Callout tone="success">{notice}</Callout>}
          <ErrorLine error={fail} />
        </div>
      )}

      <ScrollArea className="min-h-0 flex-1">
        <div className="p-2" aria-busy={firstLoad}>
          {firstLoad && <SkeletonList count={4} />}
          {!firstLoad && rows.length === 0 && (
            <EmptyState
              compact
              icon={Users}
              title="还没有工作者"
              description="方向开始执行后会出现在这里。可在图谱查看方向，或通过通信向指挥官补充任务要求。"
            />
          )}
          {!firstLoad && rows.length > 0 && filtered.length === 0 && (
            <EmptyState
              compact
              icon={Users}
              title="没有匹配的工作者"
              description={`当前过滤：${facet === 'all' ? '全部状态' : FACETS.find((f) => f.id === facet)?.label}${needle ? ` · ${query.trim()}` : ''}`}
              action={
                <Button size="sm" variant="outline" onClick={() => { setQuery(''); setFacet('all') }}>
                  清除筛选
                </Button>
              }
            />
          )}
          {/* The roster is a real list: `ul/li` gives screen readers a count and
              a "list of N items" boundary, which the old stack of articles did
              not. The live region below announces filter results. */}
          <ul role="list" className="divide-y divide-hairline" aria-label="工作者名单">
            {filtered.map((w, index) => (
              <WorkerRow
                key={w.intent_id}
                w={w}
                defer={index >= 6}
                selected={selected === w.intent_id}
                onSelect={() => setSelected(selected === w.intent_id ? null : w.intent_id)}
                onKill={() => kill(w)}
                onOpenGraphIntent={onOpenGraphIntent}
                onOpenRun={onOpenRun}
              />
            ))}
          </ul>
          <p role="status" aria-live="polite" className="sr-only">
            显示 {filtered.length} / {rows.length} 名工作者
          </p>
          {rows.length > 0 && (
            <div className="px-1.5 py-2 font-mono text-[11px] text-ink-3">
              排序：在跑 → 待办/等待 → 停/终态；名单每 4 秒刷新
            </div>
          )}
        </div>
      </ScrollArea>
    </Panel>
  )
}

function WorkerRow({
  w,
  defer,
  selected,
  onSelect,
  onKill,
  onOpenGraphIntent,
  onOpenRun,
}: {
  w: WorkerView
  /** Guide `defer-rendering-heavy-content`: only rows below the first viewport
   *  get `content-visibility: auto`; the first rows stay uncontained so the
   *  containment engine cannot slow down the initial paint. */
  defer: boolean
  selected: boolean
  onSelect: () => void
  onKill: () => void
  onOpenGraphIntent?: (intentID: string) => void
  onOpenRun?: (runID: string) => void
}) {
  const terminal = !w.can_kill
  // A 2px rail carries the semantic colour in a small area; running is the only
  // "live" tone, failed is the only alarm tone.
  const rail = w.running
    ? 'border-l-info/60'
    : w.status === 'failed'
      ? 'border-l-destructive/60'
      : 'border-l-transparent'
  return (
    // `content-visibility: auto` on a roster row is safe here: nothing in this
    // view measures row geometry (no scroll anchoring / getBoundingClientRect),
    // and the paired intrinsic size is the two-line collapsed row, so the
    // scrollbar does not jump while the list is skipped off-screen.
    <li
      data-worker-id={w.intent_id}
      data-selected={selected ? 'true' : undefined}
      className={`border-l-2 transition-colors ${defer ? '[contain-intrinsic-size:auto_3.5rem] [content-visibility:auto]' : ''} ${rail} ${selected ? 'bg-surface-3/70' : 'hover:bg-surface-2/60'}`}
    >
      <div className="flex items-stretch gap-2 px-2.5 py-2">
        <button
          type="button"
          onClick={onSelect}
          aria-expanded={selected}
          className="flex min-w-0 flex-1 flex-col justify-center gap-1 rounded-md px-1 py-0.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        >
          <span className="flex min-w-0 items-center gap-2">
            <StatusIndicator compact status={statusKind(w.status)} label={statusLabel(w.status)} />
            {w.running && <StatusIndicator compact status="running" label="生成中" />}
            {w.hold && <Badge size="sm" variant="outline">Hold</Badge>}
            {w.parent_intent_id && (
              <Badge size="sm" variant="secondary" title={`父意图 ${w.parent_intent_id}`}>
                子任务
              </Badge>
            )}
            <span className="min-w-0 truncate text-[13px] font-medium text-ink">{w.title}</span>
          </span>
          <span className="flex min-w-0 items-center gap-2 font-mono text-[11px] text-ink-3">
            <span className="min-w-0 truncate" title={w.intent_id}>{w.intent_id}</span>
            {w.run_id && (
              <>
                <span aria-hidden className="text-ink-3/60">·</span>
                <span className="min-w-0 truncate" title={w.run_id}>run {w.run_id}</span>
                {w.run_status ? <span className="shrink-0">（{statusLabel(w.run_status)}）</span> : null}
              </>
            )}
            <span className="ml-auto shrink-0">{fmtTime(w.created_at)}</span>
          </span>
        </button>
        <div className="flex shrink-0 items-center gap-1 self-center">
          {/* 跨页联动（docs/34 §4）：worker = 意图 + 最近 run，所以两个入口
              各自指向图谱的 s:<id> 与运行页的 run。回调没传时不渲染。 */}
          {onOpenGraphIntent && w.intent_id && (
            <Button
              size="xs"
              variant="ghost"
              data-testid="worker-open-graph"
              title="在图谱中选中这个方向"
              onClick={() => onOpenGraphIntent(w.intent_id)}
            >
              图谱
            </Button>
          )}
          {onOpenRun && w.run_id && (
            <Button
              size="xs"
              variant="ghost"
              data-testid="worker-open-run"
              title="在运行页打开这个 worker 的最近 run"
              onClick={() => onOpenRun(w.run_id!)}
            >
              运行
            </Button>
          )}
          <Button
            size="xs"
            variant="outline"
            className={terminal ? 'shrink-0' : 'shrink-0 border-destructive/30 text-destructive hover:border-destructive/50 hover:bg-destructive/10 hover:text-destructive'}
            disabled={terminal}
            title={terminal ? '已结束：不用再 kill' : '置 Hold + 取消当前 run（需二次确认）'}
            onClick={onKill}
          >
            <Power className="h-3 w-3" />
            kill
          </Button>
        </div>
      </div>

      {selected && (
        <div className="space-y-1.5 border-t border-hairline px-3 py-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-ink-3">
            <span>参与者 {w.participant_id}</span>
            {w.profile && <span>profile {w.profile}</span>}
            {w.resolved_profile && w.resolved_profile !== w.profile && (
              <span title="run 实际解析到的模型配置">实际 {w.resolved_profile}</span>
            )}
            {w.model && <span>model {w.model}</span>}
            {w.node && <span>node {w.node}</span>}
          </div>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-ink-3">
            {w.parent_intent_id && <span>父 {w.parent_intent_id}</span>}
            {w.delegated_by_run_id && <span>委派 run {w.delegated_by_run_id}</span>}
            {w.created_by_participant_id && <span>创建者 {w.created_by_participant_id}</span>}
            {w.last_event_kind && <span className="uppercase tracking-wide">{w.last_event_kind}</span>}
            {w.last_event_at && <span className="ml-auto">{fmtTime(w.last_event_at)}</span>}
          </div>
          <div className="flex items-start gap-2 text-[11px] leading-relaxed text-ink-2">
            <span className="shrink-0 text-ink-3">最近事件</span>
            <span className="tw-break min-w-0 flex-1">{oneLine(w.last_event_text ?? '', 220) || '—'}</span>
          </div>
        </div>
      )}
    </li>
  )
}

function PeerPolicyPanel() {
  const peers = useLoad(api.peers, [])
  const people = usePoll(api.participants, 8000)
  const [mode, setMode] = useState<'open' | 'allowlist'>('open')
  const [rows, setRows] = useState<PeerRule[]>([])
  const [busy, setBusy] = useState(false)
  const [fail, setFail] = useState<unknown>(null)
  const [notice, setNotice] = useState('')

  // The server is the source of truth; reset the editor whenever it answers.
  useEffect(() => {
    if (!peers.data) return
    setMode(peers.data.mode === 'allowlist' ? 'allowlist' : 'open')
    setRows(peers.data.peer_policy.allow ?? [])
  }, [peers.data])

  const participantRows = people.data ?? []
  const options = useMemo(() => {
    const seen = new Set(participantRows.map((p) => p.id))
    const extras = rows
      .flatMap((r) => [r.from, r.to])
      .filter((v) => v && !seen.has(v))
      .map((id) => ({ id, title: id }))
    return [...participantRows.map((p) => ({ id: p.id, title: p.title })), ...extras]
  }, [participantRows, rows])

  function addRow() {
    setRows((cur) => [...cur, { from: '', to: '' }])
  }

  function setRow(i: number, patch: Partial<PeerRule>) {
    setRows((cur) => cur.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }

  function removeRow(i: number) {
    setRows((cur) => cur.filter((_, idx) => idx !== i))
  }

  async function save() {
    if (mode === 'allowlist') {
      const incomplete = rows.some((r) => !r.from || !r.to)
      if (incomplete) {
        setFail(new Error('每条规则都要选 from 与 to；不完整的行请删除'))
        return
      }
      const self = rows.find((r) => r.from === r.to)
      if (self) {
        setFail(new Error('from 与 to 不能是同一个人（自己发给自己在服务端也会被拒）'))
        return
      }
    }
    setBusy(true)
    setFail(null)
    setNotice('')
    try {
      const clean = rows.filter((r) => r.from && r.to)
      const res = await api.savePeers({ mode, allow: mode === 'allowlist' ? clean : [] })
      peers.setData(res)
      setNotice(`已保存：mode=${res.mode}${res.mode === 'allowlist' ? `，${clean.length} 条有向规则` : ''}`)
    } catch (e) {
      setFail(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel
      title="消息授权（peer 策略）"
      extra={<ShieldCheck className="h-3.5 w-3.5 text-ink-3" />}
    >
      <div className="space-y-3">
        <Field
          label="模式"
          hint="open：同项目任意参与者可互发；allowlist：只允许下面列出的有向 from→to（commander 与发给自己除外）"
        >
          <Select value={mode} onValueChange={(v) => setMode(v as typeof mode)}>
            <SelectTrigger className="h-9 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="open" className="text-[12px]">
                open（默认，全项目可互发）
              </SelectItem>
              <SelectItem value="allowlist" className="text-[12px]">
                allowlist（只允许显式授权）
              </SelectItem>
            </SelectContent>
          </Select>
        </Field>

        {peers.error && (
          <Callout tone="destructive">
            <div className="flex flex-wrap items-center gap-2">
              <span>读 peer 策略失败：{peers.error}</span>
              <Button size="xs" variant="outline" onClick={() => peers.reload()}>重试</Button>
            </div>
          </Callout>
        )}

        {mode === 'allowlist' && (
          <div className="space-y-1.5">
            {rows.length === 0 && (
              <div className="tw-inset rounded-md px-2.5 py-2 text-[12px] text-ink-2">
                allowlist 为空 = 除 commander 通道外都不能互发。用「加一条」授权方向。
              </div>
            )}
            {rows.map((r, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <Select
                  value={r.from || undefined}
                  onValueChange={(v) => setRow(i, { from: v })}
                >
                  <SelectTrigger className="h-8 min-w-0 flex-1 text-[12px]">
                    <SelectValue placeholder="from" />
                  </SelectTrigger>
                  <SelectContent>
                    {options.map((p) => (
                      <SelectItem key={p.id} value={p.id} className="text-[12px]">
                        <span className="font-mono">{p.id}</span>
                        <span className="ml-2 text-[11px] text-ink-3">{p.title}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="shrink-0 text-[11px] text-ink-3">→</span>
                <Select value={r.to || undefined} onValueChange={(v) => setRow(i, { to: v })}>
                  <SelectTrigger className="h-8 min-w-0 flex-1 text-[12px]">
                    <SelectValue placeholder="to" />
                  </SelectTrigger>
                  <SelectContent>
                    {options.map((p) => (
                      <SelectItem key={p.id} value={p.id} className="text-[12px]">
                        <span className="font-mono">{p.id}</span>
                        <span className="ml-2 text-[11px] text-ink-3">{p.title}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  className="shrink-0"
                  onClick={() => removeRow(i)}
                  title="删除这条方向"
                  aria-label="删除这条方向"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            <div className="flex flex-wrap items-center gap-2 pt-0.5">
              <Button size="sm" variant="outline" onClick={addRow} disabled={options.length === 0}>
                加一条
              </Button>
              {options.length === 0 && (
                <span className="text-[11px] text-ink-3">
                  方向开始执行后可在这里选择参与者并设置消息授权。
                </span>
              )}
            </div>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="secondary" onClick={save} disabled={busy || !peers.data}>
            {busy && <Spinner size="sm" className="text-ink-2" />}
            {busy ? '保存中…' : '保存策略'}
          </Button>
          {notice && <span className="text-[11px] text-success">{notice}</span>}
        </div>
        <ErrorLine error={fail} />
      </div>
    </Panel>
  )
}
