// The asset tree panel (spec #1 工单7-9/#8, the operator's aligned revision):
// the same canvas, toolbar, minimap, list view and dock widths the engagement
// map uses, pointed at the project-rooted asset tree. The layered tidy-tree
// layout reads it left-to-right and symmetric — 项目卡片 → 域名/独立IP/网段 →
// 主机 → 接口 → URL — exactly like the engagement graph's 链路树.
//
// One canvas, one tree: there is no second view to switch into. Focusing a
// domain is 只看这棵子树 (the engagement graph's own subtree mechanism), and
// the table one header toggle away remains the page's other reading.
//
// The node panel is this panel's own: an asset node has fence standing, a
// functional summary, tested reading, vulnerability tags and two actions
// (移出围栏 / 添加指挥官指令), which the ledger graph's inspector does not
// know about.

import { useEffect, useMemo, useRef, useState } from 'react'
import { Focus, MessageSquarePlus, ShieldOff } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Spinner,
  Textarea,
  useConfirm,
} from '@cyber/ui'
import { GraphCanvas, type GraphControls } from '@/components/GraphCanvas'
import { GraphListView } from '@/components/graph/GraphListView'
import { GraphMinimap } from '@/components/graph/GraphMinimap'
import { GraphToolbar } from '@/components/graph/GraphToolbar'
import { DockResizeHandle, useDockSize, type DockSize } from '@/components/graph/DockResize'
import { useViewWidth, useHovered } from '@/graph/view-store'
import type { GraphModel } from '@/graph/model'
import { subtree } from '@/graph/chain'
import type { LayoutMode } from '@/graph/sim'
import { cameraStorageKey } from '@/graph/camera'
import { api } from '@/lib/api'
import { assetKindLabel, buildAssetGraphModel, filterAssetModel } from '@/lib/asset-graph'
import { FENCE_LABELS, fenceStateOf, fenceVariant } from '@/lib/assets'
import { randomRequestKey } from '@/lib/directive'
import { fmtInt, severityVariant } from '@/lib/format'
import { useLoad } from '@/lib/hooks'
import type { AssetGraph, AssetGraphNode, Instruction } from '@/lib/types'

const EMPTY_GRAPH: AssetGraph = {
  root: 'asset:project',
  nodes: [],
  edges: [],
  totals: {},
  unlinked_vulns: 0,
}

interface Props {
  current: string
}

export function AssetGraphPanel({ current }: Props) {
  const data = useLoad(() => api.assetGraph(), [])
  const graph = {
    ...EMPTY_GRAPH,
    ...(data.data ?? {}),
    nodes: data.data?.nodes ?? [],
    edges: data.data?.edges ?? [],
  }
  const model = useMemo(() => buildAssetGraphModel(data.data), [data.data])
  const canvas = useRef<GraphControls | null>(null)
  const stageWidth = useViewWidth()
  const dock = useDockSize(stageWidth)
  const [selected, setSelected] = useState<string | null>(null)
  const [focusIds, setFocusIds] = useState<ReadonlySet<string> | null>(null)
  const [listView, setListView] = useState(false)
  const [hoverPreview, setHoverPreview] = useState(true)
  const [showLabels, setShowLabels] = useState(true)
  // 链路树 is the reading view the operator aligned on (project card → domains
  // → hosts → interfaces → URLs, left to right, symmetric); the force map
  // stays one click away in the toolbar's 视图.
  const [layout, setLayout] = useState<LayoutMode>('layered')

  useEffect(() => setSelected(null), [focusIds])

  // A new tree (or a new focus) must be framed once: the layered tree's left
  // edge is the project card, and leaving the camera wherever the previous
  // model left it would open on an arbitrary corner.
  const view = useMemo(
    () => (focusIds ? filterAssetModel(model, focusIds) : model),
    [model, focusIds],
  )
  useEffect(() => {
    if (!data.data) return
    const frame = requestAnimationFrame(() => canvas.current?.fit({ margin: 0.14, capAtOne: true }))
    return () => cancelAnimationFrame(frame)
  }, [data.data, view])

  const selectedNode = selected ? (graph.nodes.find((n) => n.id === selected) ?? null) : null
  const total = graph.nodes.length

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {focusIds && (
          <Button
            size="xs"
            variant="ghost"
            className="gap-1 px-1.5"
            onClick={() => {
              setFocusIds(null)
              setSelected(null)
            }}
          >
            返回全图
          </Button>
        )}
        <span className="text-[12px] font-medium text-ink">
          {focusIds ? '只看这棵子树' : '项目资产树'}
        </span>
        <FenceTotals totals={graph.totals} />
        {graph.unlinked_vulns > 0 && (
          <a href="?view=vulns" className="ml-auto text-[11px] text-warning underline-offset-2 hover:underline">
            {graph.unlinked_vulns} 个未关联资产的漏洞
          </a>
        )}
      </div>

      {data.error && (
        <Callout tone="destructive">
          {data.error}
          <Button size="xs" variant="outline" className="ml-2" onClick={data.reload}>
            重试
          </Button>
        </Callout>
      )}

      <section
        aria-label="资产图谱"
        className="zrcin-graph-stage relative h-[min(760px,calc(100dvh-12rem))] min-h-[420px] overflow-hidden rounded-xl border border-hairline bg-surface-1"
      >
        <GraphCanvas
          ref={canvas}
          model={view}
          selected={selected}
          matches={null}
          showLabels={showLabels}
          rightInset={selectedNode ? dock.width : 0}
          layout={layout}
          cameraKey={cameraStorageKey(`${current}:assets-tree`)}
          ready={!data.loading}
          showOriginLinks={false}
          hoverPreview={hoverPreview}
          onSelect={setSelected}
        />

        {!listView && (
          <GraphToolbar
            chatDock={false}
            onToggleChatDock={() => {}}
            layout={layout}
            onLayout={setLayout}
            showLabels={showLabels}
            onToggleLabels={() => setShowLabels((v) => !v)}
            listView={listView}
            onToggleListView={() => setListView((v) => !v)}
            onZoom={(factor) => canvas.current?.zoom(factor)}
            onFit={() => canvas.current?.fit()}
            onRelayout={() => canvas.current?.relayout()}
            timelineOpen={false}
            onToggleTimeline={() => {}}
            hoverPreview={hoverPreview}
            onToggleHoverPreview={() => setHoverPreview((v) => !v)}
            hideChat
            hideTimeline
            labelsTitle="节点标签"
          />
        )}

        {!selectedNode && !listView && (
          <div className="zrcin-graph-corner-br absolute flex flex-col items-end gap-1.5">
            <div className="zrcin-graph-minimap">
              <GraphMinimap
                model={view}
                layout={layout}
                onCenter={(x, y) => canvas.current?.centerOn(x, y)}
                onFit={() => canvas.current?.fit()}
                showOriginLinks={false}
              />
            </div>
          </div>
        )}

        {selectedNode && (
          <AssetNodePanel
            node={selectedNode}
            model={view}
            dock={dock}
            onClose={() => setSelected(null)}
            onFocus={(id) => {
              setFocusIds(subtree(view, id))
              setSelected(null)
            }}
            onChanged={data.reload}
          />
        )}

        {hoverPreview && !selectedNode && !listView && (
          <AssetHoverCard model={view} graph={graph} selected={selected} width={dock.width} />
        )}

        {listView && (
          <GraphListView
            model={view}
            selected={selected}
            matches={null}
            onSelect={(id) => setSelected(id)}
            onClose={() => setListView(false)}
          />
        )}

        {!data.loading && total === 0 && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
            <div className="pointer-events-auto rounded-xl border border-hairline bg-card/95 px-4 py-3 text-center shadow-elevated">
              <p className="text-[12.5px] font-medium text-ink">还没有可画的资产</p>
              <p className="mt-1 text-[11.5px] leading-5 text-ink-3">
                创建项目时填的围栏、导入的扫描结果与执行轮捕获的流量都会落成资产节点。
              </p>
            </div>
          </div>
        )}
        {data.loading && !data.data && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
            <div role="status" className="rounded-full border border-hairline bg-card/90 px-3.5 py-1.5 text-[11px] text-ink-3 shadow-soft backdrop-blur">
              正在读取资产树…
            </div>
          </div>
        )}
      </section>
    </div>
  )
}

/** The fence legend: the counts the fill colours stand for. One quiet line, no
 *  second card. */
function FenceTotals({ totals }: { totals: Record<string, number> }) {
  const order: Array<{ id: string; dot: string }> = [
    { id: 'in_fence', dot: 'bg-success' },
    { id: 'pending', dot: 'bg-warning' },
    { id: 'denied', dot: 'bg-primary' },
    { id: 'unregistered', dot: 'bg-ink-3' },
  ]
  return (
    <span className="flex flex-wrap items-center gap-3 text-[11px] text-ink-3">
      {order.map((item) => (
        <span key={item.id} className="inline-flex items-center gap-1">
          <span className={`h-1.5 w-1.5 rounded-full ${item.dot}`} aria-hidden="true" />
          {FENCE_LABELS[item.id as keyof typeof FENCE_LABELS]}
          <span className="zrcin-nums font-mono text-ink-2">{fmtInt(totals[item.id] ?? 0)}</span>
        </span>
      ))}
    </span>
  )
}

function AssetNodePanel({
  node,
  model,
  dock,
  onClose,
  onFocus,
  onChanged,
}: {
  node: AssetGraphNode
  model: GraphModel
  dock: DockSize
  onClose: () => void
  onFocus: (id: string) => void
  onChanged: () => void
}) {
  const confirm = useConfirm()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [receipt, setReceipt] = useState('')
  const [instructionOpen, setInstructionOpen] = useState(false)
  const instructions = useLoad(() => api.instructions('all'), [])
  const assetKey = node.host ?? node.label
  const isProject = node.kind === 'project'
  const isAsset = Boolean(node.asset_id) && (node.kind === 'domain' || node.kind === 'host' || node.kind === 'range')
  const canFocus = (model.outgoing.get(node.id)?.length ?? 0) > 0
  const related = useMemo(
    () => (instructions.data?.instructions ?? []).filter((row) => row.asset_ref && row.asset_ref === assetKey),
    [instructions.data, assetKey],
  )

  async function leaveFence() {
    if (!node.asset_id || busy) return
    const ok = await confirm({
      title: `将 ${assetKey} 移出围栏？`,
      description:
        '移出后系统会登记一条停测指令并停掉这个资产正在运行的意图；之后对它的请求按围栏外处理，停测之后才到达的事实会被标注为「停测后」，而不是当作已有结论。',
      confirmLabel: '移出围栏',
      destructive: true,
    })
    if (!ok) return
    setBusy(true)
    setError('')
    setReceipt('')
    try {
      await api.changeAssetFence(node.asset_id, 'leave')
      setReceipt(`已移出围栏：${assetKey} 的停测指令已登记，相关意图已停。`)
      instructions.reload()
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="zrcin-graph-dock zrcin-graph-dock-right zrcin-graph-panel zrcin-graph-under-toolbar absolute flex flex-col overflow-hidden rounded-xl border border-hairline bg-card/95 shadow-elevated backdrop-blur"
      style={{ width: dock.width }}
      data-testid="asset-node-panel"
    >
      <DockResizeHandle dock={dock} />
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline bg-surface-1/60 px-4 py-2.5">
        <Badge size="sm" variant="secondary">{assetKindLabel(node.kind)}</Badge>
        {node.fence !== undefined && (
          <Badge size="sm" variant={fenceVariant(node.fence)}>{FENCE_LABELS[fenceStateOf({ fence: node.fence })]}</Badge>
        )}
        {node.tested && <Badge size="sm" variant="muted">{node.tested === 'concluded' ? '有结论' : node.tested === 'reached' ? '已触达' : '未触达'}</Badge>}
        <Button size="xs" variant="ghost" className="ml-auto px-1.5" onClick={onClose}>
          收起
        </Button>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain px-4 py-3 [scrollbar-width:thin]">
        <p className="zrcin-break font-mono text-[13px] leading-5 text-ink">{node.label}</p>
        {node.sub && node.sub !== node.label && (
          <div>
            <div className="zrcin-eyebrow mb-1">功能作用</div>
            <p className="zrcin-break text-[11.5px] leading-5 text-ink-2">{node.sub}</p>
          </div>
        )}
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11.5px] text-ink-2">
          {typeof node.requests === 'number' && node.requests > 0 && (
            <span><span className="zrcin-nums font-mono">{node.requests}</span> 请求</span>
          )}
          {typeof node.urls === 'number' && node.urls > 0 && (
            <span><span className="zrcin-nums font-mono">{node.urls}</span> URL</span>
          )}
        </div>

        {canFocus && (
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => onFocus(node.id)}>
            <Focus className="h-3.5 w-3.5" /> 只看这棵子树
          </Button>
        )}

        {!isProject && (
          <div className="flex flex-wrap gap-1.5">
            <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setInstructionOpen(true)}>
              <MessageSquarePlus className="h-3.5 w-3.5" /> 添加指挥官指令
            </Button>
            {isAsset && node.fence !== 'denied' && (
              <Button
                size="sm"
                variant="outline"
                className="gap-1.5 text-destructive hover:text-destructive"
                disabled={busy}
                onClick={() => void leaveFence()}
              >
                {busy ? <Spinner size="sm" /> : <ShieldOff className="h-3.5 w-3.5" />}
                {busy ? '移出中…' : '移出围栏'}
              </Button>
            )}
          </div>
        )}
        {error && <Callout tone="destructive">{error}</Callout>}
        {receipt && <p className="text-[11.5px] leading-5 text-ink-2">{receipt}</p>}

        {(node.vuln_tags?.length ?? 0) > 0 && (
          <section aria-label="漏洞标签">
            <div className="zrcin-eyebrow mb-1">漏洞标签（{node.vuln_tags!.length}）</div>
            <ul role="list" className="divide-y divide-hairline rounded-lg border border-hairline">
              {node.vuln_tags!.map((tag) => (
                <li key={tag.id} className="flex flex-wrap items-center gap-2 px-2.5 py-1.5">
                  <span className="min-w-0 flex-1 text-[11.5px] text-ink">{tag.title}</span>
                  {tag.severity && <Badge size="sm" variant={severityVariant(tag.severity)}>{tag.severity}</Badge>}
                </li>
              ))}
            </ul>
          </section>
        )}

        <AssetInstructionList rows={related} loading={instructions.loading} error={instructions.error} />
      </div>
      {instructionOpen && (
        <AssetInstructionDialog
          assetRef={assetKey}
          onClose={() => setInstructionOpen(false)}
          onCreated={() => {
            setInstructionOpen(false)
            instructions.reload()
          }}
        />
      )}
    </div>
  )
}

function AssetInstructionList({ rows, loading, error }: { rows: Instruction[]; loading: boolean; error?: string }) {
  return (
    <div className="border-t border-hairline pt-2">
      <div className="zrcin-eyebrow mb-1">针对该资产的指令（{rows.length}）</div>
      {error && <p className="text-[11px] text-destructive">读指令失败：{error}</p>}
      {loading && rows.length === 0 && <p className="text-[11px] text-ink-3">读取中…</p>}
      {!loading && !error && rows.length === 0 && (
        <p className="text-[11px] leading-4 text-ink-3">还没有按引用绑定到它的指令。</p>
      )}
      <ul role="list" className="space-y-1.5">
        {rows.map((row) => (
          <li key={row.id} className="rounded-md border border-hairline bg-surface-2/40 px-2 py-1.5">
            <div className="flex items-center gap-1.5 text-[10.5px] text-ink-3">
              <Badge size="sm" variant={row.state === 'revoked' ? 'muted' : 'success'}>
                {row.state === 'revoked' ? '已撤销' : '生效中'}
              </Badge>
              <span className="zrcin-nums ml-auto font-mono">v{row.version}</span>
            </div>
            <p className="zrcin-break mt-1 whitespace-pre-wrap text-[11px] leading-4 text-ink-2">{row.text}</p>
          </li>
        ))}
      </ul>
    </div>
  )
}

function AssetInstructionDialog({
  assetRef,
  onClose,
  onCreated,
}: {
  assetRef: string
  onClose: () => void
  onCreated: () => void
}) {
  const [text, setText] = useState(`针对资产 ${assetRef}：`)
  const [key] = useState(() => randomRequestKey())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function submit() {
    if (busy) return
    const body = text.trim()
    if (!body) {
      setError('写一条指令再提交。')
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.createAssetInstruction({ text: body, asset_ref: assetRef, idempotency_key: key })
      onCreated()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>添加指挥官指令</DialogTitle>
          <DialogDescription>
            指令按引用绑定到 <span className="font-mono">{assetRef}</span>，持续注入以后每轮任务书。它是指导，不改变授权边界。
          </DialogDescription>
        </DialogHeader>
        <Textarea
          rows={4}
          value={text}
          disabled={busy}
          onChange={(e) => setText(e.target.value)}
          aria-label="资产指令正文"
          className="min-h-[96px] text-[12.5px] leading-relaxed"
        />
        {error && <Callout tone="destructive">{error}</Callout>}
        <DialogFooter>
          <Button size="sm" variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={busy}>
            {busy && <Spinner size="sm" className="mr-1" />}
            {busy ? '保存中…' : '保存并持续生效'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** The canvas's hover card, asset-shaped: what the pointer is on, its standing
 *  and its counts. It never covers the pinned panel. */
function AssetHoverCard({
  model,
  graph,
  selected,
  width,
}: {
  model: GraphModel
  graph: AssetGraph
  selected: string | null
  width: number
}) {
  const hovered = useHovered()
  if (!hovered || hovered === selected) return null
  const node = model.byId.get(hovered)
  if (!node) return null
  const asset = graph.nodes.find((n) => n.id === hovered)
  return (
    <div
      className="pointer-events-none zrcin-graph-dock zrcin-graph-dock-right zrcin-graph-under-toolbar absolute flex flex-col gap-1 rounded-xl border border-hairline bg-card/95 px-3 py-2 shadow-elevated backdrop-blur"
      style={{ width }}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge size="sm" variant="secondary">{assetKindLabel(asset?.kind ?? node.kind)}</Badge>
        {asset?.fence !== undefined && (
          <Badge size="sm" variant={fenceVariant(asset.fence)}>{FENCE_LABELS[fenceStateOf({ fence: asset.fence })]}</Badge>
        )}
        {asset?.tested && <Badge size="sm" variant="muted">{asset.tested === 'concluded' ? '有结论' : asset.tested === 'reached' ? '已触达' : '未触达'}</Badge>}
        {(asset?.vuln_tags?.length ?? 0) > 0 && (
          <Badge size="sm" variant="warning">{asset!.vuln_tags!.length} 漏洞</Badge>
        )}
      </div>
      <p className="zrcin-break font-mono text-[12px] leading-4 text-ink">{node.label}</p>
      {asset?.sub && asset.sub !== asset.label && (
        <p className="zrcin-break font-mono text-[10.5px] leading-4 text-ink-3">{asset.sub}</p>
      )}
    </div>
  )
}
