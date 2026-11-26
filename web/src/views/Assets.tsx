import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, Boxes, Network, Search, ShieldAlert, Table2 } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  EmptyState,
  Input,
  SkeletonList,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  ToggleGroup,
  ToggleGroupItem,
} from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { AssetGraphPanel } from '@/components/assets/AssetGraphPanel'
import { api } from '@/lib/api'
import {
  FENCE_LABELS,
  fenceStateOf,
  fenceVariant,
  filterAssetRows,
  kindLabel,
  statusSummary,
  testedOf,
  type FenceFilter,
} from '@/lib/assets'
import { fmtTime, severityVariant } from '@/lib/format'
import { useDebounced, useLoad, usePoll } from '@/lib/hooks'
import type { AssetDetail, AssetRow, VulnEntry } from '@/lib/types'

const FENCE_CHIPS: { id: FenceFilter; label: string }[] = [
  { id: '', label: '全部' },
  { id: 'in_fence', label: FENCE_LABELS.in_fence },
  { id: 'pending', label: FENCE_LABELS.pending },
  { id: 'denied', label: FENCE_LABELS.denied },
  { id: 'unregistered', label: FENCE_LABELS.unregistered },
]

/**
 * The asset page (spec #1 工单6/#7): one row per domain — the registrable
 * domain, with its observed and fenced hosts — and a drill-down sheet showing
 * the row's hosts, per-URL traffic with the newest fence verdict, and the
 * vulnerability entries that name one of its hosts. The three-state test
 * reading is the server's: untouched, reached, concluded; the page never
 * upgrades one into another.
 *
 * The unlinked vulnerability entries (no asset identity in the declaration)
 * sit apart at the bottom rather than being guessed onto a row.
 */
export function Assets() {
  const [query, setQuery] = useState('')
  const search = useDebounced(query, 200)
  const [fence, setFence] = useState<FenceFilter>('')
  const [view, setView] = useState<'table' | 'graph'>('table')
  // The drilled row. Empty = the full project view; the switch above the canvas
  // says which one is on screen, so the page never shows two maps.
  const [graphKey, setGraphKey] = useState('')
  const table = usePoll(api.assetsTable, 10_000)
  const rows = table.data?.rows ?? []
  const filtered = useMemo(() => filterAssetRows(rows, { query: search, fence }), [rows, search, fence])
  const [selected, setSelected] = useState<string>('')
  const [unlinkedOpen, setUnlinkedOpen] = useState(false)
  const unlinked = table.data?.unlinked_vulns ?? 0

  // A selection that the filter (or a reload) no longer shows must not leave
  // a stale sheet open over a different table.
  useEffect(() => {
    if (selected && !filtered.some((row) => row.key === selected)) setSelected('')
  }, [filtered, selected])

  const openGraph = (key: string) => {
    setSelected('')
    setGraphKey(key)
    setView('graph')
  }

  return (
    <Page
      title="资产"
      subtitle={`${rows.length} 个域名与网段 · 待审 ${rows.filter((r) => r.fence === 'pending').length}`}
      actions={
        <div className="flex flex-wrap items-center gap-1.5">
          <ToggleGroup
            value={view}
            onValueChange={(value) => setView(value as 'table' | 'graph')}
            ariaLabel="资产视图"
          >
            <ToggleGroupItem value="table" aria-label="表格视图">
              <Table2 className="mr-1 h-3.5 w-3.5" /> 表格
            </ToggleGroupItem>
            <ToggleGroupItem value="graph" aria-label="图谱视图">
              <Network className="mr-1 h-3.5 w-3.5" /> 图谱
            </ToggleGroupItem>
          </ToggleGroup>
          {view === 'table' && (
            <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="围栏筛选">
              {FENCE_CHIPS.map((chip) => (
                <Button
                  key={chip.id || 'all'}
                  size="sm"
                  variant={fence === chip.id ? 'default' : 'outline'}
                  aria-pressed={fence === chip.id}
                  onClick={() => setFence(chip.id)}
                >
                  {chip.label}
                </Button>
              ))}
            </div>
          )}
        </div>
      }
    >
      {view === 'graph' ? (
        <AssetGraphPanel
          current={location.pathname}
          full={!graphKey}
          rowKey={graphKey || undefined}
          onDrill={(key) => setGraphKey(key)}
          onBack={() => setGraphKey('')}
        />
      ) : (
        <div className="space-y-3">
          <div className="relative max-w-xl">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-3" />
            <Input
              aria-label="筛选域名或主机"
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="筛选域名、IP 或主机"
              className="h-8 pl-8 text-[12px]"
              inputMode="search"
              enterKeyHint="search"
              autoComplete="off"
              spellCheck={false}
            />
          </div>

          {table.error && (
            <Callout tone="destructive">
              {table.error}
              <Button size="xs" variant="outline" className="ml-2" onClick={table.reload}>
                重试
              </Button>
            </Callout>
          )}

          <Panel title="资产表" bodyClassName="p-0">
            {table.loading && (
              <div className="p-4">
                <SkeletonList count={6} />
              </div>
            )}
            {!table.loading && filtered.length === 0 && (
              <EmptyState
                icon={Boxes}
                title={rows.length === 0 ? '还没有资产' : '没有匹配的资产'}
                description={
                  rows.length === 0
                    ? '创建项目时填的围栏、导入的扫描结果与执行轮捕获的流量都会落成资产。'
                    : '换个关键词或围栏筛选。'
                }
              />
            )}
            {filtered.length > 0 && (
              <div
                role="region"
                aria-label="资产表"
                tabIndex={0}
                className="zrcin-scroll max-h-[min(640px,72vh)] overflow-auto overscroll-contain [scrollbar-width:thin] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset"
              >
                <table className="w-full min-w-0 border-separate border-spacing-0 text-[12px]">
                  <caption className="sr-only">每行一个域名或网段：主机、URL、请求、围栏、漏洞、测试与最近活动</caption>
                  <thead className="sticky top-0 z-10 bg-surface-2 text-left text-[11px] font-medium text-ink-3">
                    <tr>
                      <th scope="col" className="border-b border-hairline px-3 py-2 font-medium">域名 / 网段</th>
                      <th scope="col" className="hidden w-[64px] border-b border-hairline px-3 py-2 text-right font-medium sm:table-cell">主机</th>
                      <th scope="col" className="hidden w-[64px] border-b border-hairline px-3 py-2 text-right font-medium sm:table-cell">URL</th>
                      <th scope="col" className="hidden w-[160px] border-b border-hairline px-3 py-2 font-medium md:table-cell">请求 · 状态</th>
                      <th scope="col" className="w-[88px] border-b border-hairline px-3 py-2 font-medium">围栏</th>
                      <th scope="col" className="hidden w-[64px] border-b border-hairline px-3 py-2 text-right font-medium sm:table-cell">漏洞</th>
                      <th scope="col" className="w-[88px] border-b border-hairline px-3 py-2 font-medium">测试</th>
                      <th scope="col" className="hidden w-[120px] border-b border-hairline px-3 py-2 font-medium lg:table-cell">最近活动</th>
                      <th scope="col" className="w-10 border-b border-hairline px-2 py-2"><span className="sr-only">打开</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((row) => (
                      <AssetTableRow key={row.kind + '/' + row.key} row={row} onOpen={() => setSelected(row.key)} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>

          {unlinked > 0 && (
            <button
              type="button"
              onClick={() => setUnlinkedOpen(true)}
              className="flex w-full items-center gap-2 rounded-lg border border-hairline bg-surface-2/40 px-3 py-2.5 text-left text-[12px] text-ink-2 transition-colors hover:bg-surface-2"
            >
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-warning" aria-hidden="true" />
              <span>
                {unlinked} 个未关联资产的漏洞：声明里没有资产标识，归在这里而不是按标题猜归属
              </span>
              <span className="ml-auto text-[11px] text-ink-3">查看</span>
            </button>
          )}
        </div>
      )}

      {selected && (
        <AssetDetailSheet
          rowKey={selected}
          onClose={() => setSelected('')}
          onGraph={() => openGraph(selected)}
        />
      )}
      {unlinkedOpen && <UnlinkedVulnsSheet onClose={() => setUnlinkedOpen(false)} />}
    </Page>
  )
}

function AssetTableRow({ row, onOpen }: { row: AssetRow; onOpen: () => void }) {
  const tested = testedOf(row)
  const statuses = statusSummary(row.statuses)
  return (
    <tr
      className="cursor-pointer border-b border-hairline last:border-0 hover:bg-surface-2"
      onClick={onOpen}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onOpen()
        }
      }}
      aria-label={`打开 ${row.key} 的资产详情`}
    >
      <td className="px-3 py-2.5">
        <span className="zrcin-break font-mono text-[13px] font-medium text-ink">{row.key}</span>
        <span className="ml-2 text-[11px] text-ink-3">{kindLabel(row.kind)}</span>
      </td>
      <td className="zrcin-nums hidden px-3 py-2.5 text-right font-mono text-[11px] text-ink-2 sm:table-cell">{row.host_count}</td>
      <td className="zrcin-nums hidden px-3 py-2.5 text-right font-mono text-[11px] text-ink-2 sm:table-cell">{row.urls}</td>
      <td className="hidden px-3 py-2.5 text-[11px] text-ink-2 md:table-cell">
        <span className="zrcin-nums font-mono">{row.requests}</span>
        {statuses && <span className="ml-2 font-mono text-[10px] text-ink-3">{statuses}</span>}
      </td>
      <td className="px-3 py-2.5">
        <Badge size="sm" variant={fenceVariant(row.fence)}>
          {FENCE_LABELS[fenceStateOf(row)]}
        </Badge>
      </td>
      <td className="zrcin-nums hidden px-3 py-2.5 text-right font-mono text-[11px] text-ink-2 sm:table-cell">
        {row.vulns > 0 ? <span className="text-warning">{row.vulns}</span> : 0}
      </td>
      <td className="px-3 py-2.5">
        <Badge size="sm" variant={tested.variant}>
          {tested.label}
        </Badge>
      </td>
      <td className="hidden px-3 py-2.5 font-mono text-[11px] text-ink-3 lg:table-cell">
        {row.last_seen ? fmtTime(row.last_seen) : '—'}
      </td>
      <td className="px-2 py-2.5">
        <Button
          size="xs"
          variant="ghost"
          className="px-1.5"
          aria-label={`打开 ${row.key} 的资产详情`}
          onClick={(e) => {
            e.stopPropagation()
            onOpen()
          }}
        >
          查看
        </Button>
      </td>
    </tr>
  )
}

function AssetDetailSheet({ rowKey, onClose, onGraph }: { rowKey: string; onClose: () => void; onGraph: () => void }) {
  const detail = useLoad(() => api.assetDetail(rowKey), [rowKey])
  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-3xl">
        <SheetHeader className="border-b border-hairline px-4 py-3">
          <div className="flex items-center gap-2">
            <SheetTitle className="zrcin-break min-w-0 flex-1 font-mono text-[14px]">{rowKey}</SheetTitle>
            <Button size="xs" variant="outline" className="gap-1" onClick={onGraph}>
              <Network className="h-3.5 w-3.5" /> 图谱
            </Button>
          </div>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4 [scrollbar-width:thin]">
          {detail.loading && <SkeletonList count={5} />}
          {detail.error && <Callout tone="destructive">{detail.error}</Callout>}
          {detail.data && <DetailBody detail={detail.data} />}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function DetailBody({ detail }: { detail: AssetDetail }) {
  const { row } = detail
  const tested = testedOf(row)
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge size="sm" variant={fenceVariant(row.fence)}>{FENCE_LABELS[fenceStateOf(row)]}</Badge>
        <Badge size="sm" variant={tested.variant}>{tested.label}</Badge>
        {row.target !== undefined && (
          <Badge size="sm" variant="muted">{row.target ? '目标' : '非目标'}</Badge>
        )}
        <span className="text-[11px] text-ink-3">{kindLabel(row.kind)} · {row.host_count} 主机 · {row.requests} 请求</span>
      </div>

      <section aria-label="主机">
        <h3 className="zrcin-section-title mb-1.5">主机（{detail.hosts.length}）</h3>
        <ul role="list" className="zrcin-inset divide-y divide-hairline rounded-lg">
          {detail.hosts.map((host) => (
            <li key={host.host} className="flex flex-wrap items-center gap-2 px-3 py-2">
              <span className="zrcin-break font-mono text-[12px] text-ink">{host.host}</span>
              <Badge size="sm" variant={fenceVariant(host.fence)}>{FENCE_LABELS[fenceStateOf(host)]}</Badge>
              <span className="zrcin-nums ml-auto font-mono text-[11px] text-ink-3">
                {host.requests} 请求
                {host.intents?.length ? ` · ${host.intents.length} 意图` : ''}
              </span>
              {host.vulns > 0 && <Badge size="sm" variant="warning">{host.vulns} 漏洞</Badge>}
            </li>
          ))}
        </ul>
      </section>

      <section aria-label="URL">
        <h3 className="zrcin-section-title mb-1.5">URL（{detail.urls.length}）</h3>
        {detail.urls.length === 0 ? (
          <p className="text-[12px] text-ink-3">还没有观测到请求：执行轮捕获流量后出现。</p>
        ) : (
          <ul role="list" className="divide-y divide-hairline rounded-lg border border-hairline">
            {detail.urls.map((u) => (
              <li key={u.url} className="px-3 py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="zrcin-break min-w-0 flex-1 font-mono text-[11px] leading-5 text-ink">{u.url}</span>
                  {u.verdict && (
                    <Badge
                      size="sm"
                      variant={u.verdict === 'block' ? 'destructive' : u.verdict === 'review' ? 'warning' : 'muted'}
                      title={u.rule_id ? `规则 ${u.rule_id}` : undefined}
                    >
                      {u.verdict === 'block' ? '拒绝' : u.verdict === 'review' ? (u.default_missed ? '范围外' : '待审') : '放行'}
                    </Badge>
                  )}
                </div>
                <p className="zrcin-nums mt-0.5 font-mono text-[11px] text-ink-3">
                  {u.requests} 请求{summarySuffix(u.statuses)}
                  {u.intents?.length ? ` · 意图 ${u.intents.join(', ')}` : ''}
                </p>
                {u.rule_text && <p className="zrcin-break mt-0.5 font-mono text-[10px] text-ink-3">规则：{u.rule_text}</p>}
                {(u.last_status !== undefined || u.last_error) && (
                  <p className="zrcin-break mt-0.5 font-mono text-[10px] text-ink-3">
                    最近响应：{u.last_error ? `传输错误 ${u.last_error}` : `状态 ${u.last_status}`}
                    {typeof u.last_bytes === 'number' ? ` · ${u.last_bytes.toLocaleString()} B` : ''}
                  </p>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {detail.evidence_ids && detail.evidence_ids.length > 0 && (
        <section aria-label="证据">
          <h3 className="zrcin-section-title mb-1.5">证据（{detail.evidence_ids.length}）</h3>
          <ul role="list" className="zrcin-inset divide-y divide-hairline rounded-lg">
            {detail.evidence_ids.map((id) => (
              <li key={id} className="px-3 py-2 font-mono text-[11px]">
                <a href={api.evidenceURL(id)} className="text-primary underline-offset-2 hover:underline">{id}</a>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section aria-label="漏洞">
        <h3 className="zrcin-section-title mb-1.5">漏洞（{detail.vulns.length}）</h3>
        {detail.vulns.length === 0 ? (
          <p className="text-[12px] text-ink-3">没有漏洞条目关联到这些主机。</p>
        ) : (
          <ul role="list" className="divide-y divide-hairline rounded-lg border border-hairline">
            {detail.vulns.map((v) => <VulnLine key={v.id} entry={v} />)}
          </ul>
        )}
      </section>
    </div>
  )
}

function UnlinkedVulnsSheet({ onClose }: { onClose: () => void }) {
  const entries = useLoad(() => api.unlinkedVulns(), [])
  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-x-0 border-t-0 border-b border-hairline px-4 py-3">
          <SheetTitle className="flex items-center gap-2 text-[14px]">
            <ShieldAlert className="h-4 w-4 text-warning" /> 未关联资产的漏洞
          </SheetTitle>
        </SheetHeader>
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain p-4 [scrollbar-width:thin]">
          <p className="text-[12px] leading-relaxed text-ink-2">
            这些漏洞条目的声明里没有资产标识（entity_ref）。页面不按标题或正文猜归属——补上声明后它们会出现在对应资产下。
          </p>
          {entries.loading && <SkeletonList count={4} />}
          {entries.error && <Callout tone="destructive">{entries.error}</Callout>}
          {entries.data && entries.data.vulns.length === 0 && (
            <p className="text-[12px] text-ink-3">没有未关联的条目。</p>
          )}
          {entries.data && entries.data.vulns.length > 0 && (
            <ul role="list" className="divide-y divide-hairline rounded-lg border border-hairline">
              {entries.data.vulns.map((v) => <VulnLine key={v.id} entry={v} />)}
            </ul>
          )}
          <a href="?view=vulns" className="text-[12px] text-primary underline-offset-2 hover:underline">
            在漏洞页查看全部条目
          </a>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function summarySuffix(statuses: Record<string, number> | undefined): string {
  const s = statusSummary(statuses)
  return s ? ` · ${s}` : ''
}

function VulnLine({ entry }: { entry: VulnEntry }) {
  const severity = entry.severity
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 text-[12px] text-ink">{entry.title}</span>
        {severity && <Badge size="sm" variant={severityVariant(severity)}>{severity}</Badge>}
        <Badge size="sm" variant="muted">{entry.status}</Badge>
      </div>
      <p className="zrcin-break mt-0.5 font-mono text-[11px] text-ink-3">{entry.entity_ref ?? '—'}</p>
      {entry.evidence && entry.evidence.length > 0 && (
        <p className="mt-0.5 flex flex-wrap gap-2 font-mono text-[11px]">
          {entry.evidence.slice(0, 3).map((path) => (
            <a key={path} href={api.vulnEvidenceURL(path)} className="zrcin-break text-primary underline-offset-2 hover:underline">
              {path.split('/').pop()}
            </a>
          ))}
        </p>
      )}
    </li>
  )
}
