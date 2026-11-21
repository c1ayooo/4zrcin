// The graph page body (T6, docs/33 §4.6). Everything the old GraphView rendered
// inside its `<Page>` lives here: canvas, filters, toolbar, legend, goals card,
// hover/pinned inspectors, chat dock, empty states. The shell (GraphView.tsx)
// keeps the rail, the other panels, project chrome, selection + hash deep link
// and the create-intent dialog.
//
// The snapshot itself is owned by the shell (`useGraphSnapshot(graphActive,
// current)`) because it must survive leaving the page without refetching; this
// component receives it as a prop. Leaving the page unmounts GraphPanel, so all
// polls (runs/changes) stop with the map, as before.

import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Crosshair, Download, Plus, RefreshCw } from 'lucide-react'
import { Button, EmptyState, buttonVariants } from '@cyber/ui'
import { CommanderCard } from '@/components/CommanderCard'
import { CreateChildIntentDialog } from '@/components/CreateChildIntentDialog'
import { GoalEditorDialog, type EditableGoal } from '@/components/GoalEditorDialog'
import { GraphCanvas, type GraphControls } from '@/components/GraphCanvas'
import { GraphChatDock } from '@/components/GraphChatDock'
import { Page } from '@/components/Page'
import { ProjectTokens } from '@/components/TokenUsage'
import { cameraStorageKey } from '@/graph/camera'
import { useViewWidth } from '@/graph/view-store'
import type { GraphNode } from '@/graph/model'
import type { LayoutMode } from '@/graph/sim'
import { buildTimelineFromSnapshot, revealedCount } from '@/graph/timeline'
import { chainClosure, mainPath } from '@/graph/model'
import { fmtTime } from '@/lib/format'
import { hasNewContent, liveReloadDelay, shouldLiveReload } from '@/lib/graph-live'
import { graphSelectionRoot } from '@/lib/graph-pages'
import { useSessionState } from '@/lib/session-state'
import { api, type ChangePage } from '@/lib/api'
import type { useGraphSnapshot } from '@/lib/graph-data'
import { useDebounced, useLoad, usePoll } from '@/lib/hooks'
import type { BoardResponse, ProjectInfo } from '@/lib/types'
import { GraphFilters } from './GraphFilters'
import { GraphHoverInspector } from './GraphHoverInspector'
import { GraphLegend } from './GraphLegend'
import { GraphListView } from './GraphListView'
import { GraphMinimap } from './GraphMinimap'
import { CHAIN_CAP, GraphChainStrip, defaultChainMode } from './GraphChainStrip'
import { GraphTimeline } from './GraphTimeline'
import { GraphToolbar } from './GraphToolbar'
import { useDockSize } from './DockResize'
import { defaultGraphFilters, useGraphModel, type GraphFiltersState } from './use-graph-model'
import { useFocusLayer } from './use-focus-layer'

/** The ledger snapshot lives in the shell; props stay structural so this file
 *  does not need to re-export T2's hook type. */
export type GraphSnapshot = ReturnType<typeof useGraphSnapshot>

/** The whole axis plays in this long; one click is one replay, no speed control. */
const TIMELINE_PLAY_MS = 60_000

// The pinned node panel is ~800 lines of detail/actions. Lazy loading it keeps
// the GraphView chunk inside the P1-c budget; the map is usable before it lands.
// The chunk is warmed as soon as the map mounts (below), so the download is a
// background detail rather than something the first node click waits on.
const GraphNodePanel = lazy(() => import('./GraphNodePanel'))

interface GraphPanelProps {
  current: string
  project?: ProjectInfo
  board?: BoardResponse
  graph: GraphSnapshot
  selected: string | null
  /** Shell-owned selection: writes the hash deep link. */
  onSelect: (id: string | null) => void
  /** Switch the shell to another panel (overview / messages / decisions …). */
  onNavigate: (view: string) => void
  onCreateIntent: () => void
  /** Shell refresh: reload the snapshot and the shared board/projects chrome. */
  onRefresh: () => void
  /** Runs poll errors are part of the page chrome; the shell shows them. */
  onError: (error: string | undefined) => void
}

export function GraphPanel({
  current,
  project,
  board,
  graph,
  selected,
  onSelect,
  onNavigate,
  onCreateIntent,
  onRefresh,
  onError,
}: GraphPanelProps) {
  const canvas = useRef<GraphControls | null>(null)
  // Warm the node inspector's chunk while the operator is still reading the map.
  // A click on a node must open the dock in the same frame; before this, the
  // first pin suspended on a download whose fallback is `null` (see below), so
  // the click looked ignored until the file landed.
  useEffect(() => {
    void import('./GraphNodePanel')
  }, [])
  // The right side of the map (pinned node panel, 下发 dock, hover card) shares
  // one width; the operator sets it by dragging the panel's left edge. It lives
  // here, above all three, because they take turns occupying the same corner.
  // The width is clamped against the *map* (published by the canvas), not the
  // window: the rail and the dock itself leave far less room than innerWidth.
  const stageWidth = useViewWidth()
  const dock = useDockSize(stageWidth)
  // The runs poll is the only source of per-direction warnings, the resolved
  // profile and the model each direction actually ran on. It stops with the
  // page (this component unmounts when the rail moves to another view).
  const runs = usePoll(api.runs, 6000)
  const connection = useLoad(api.settings, [])
  // A graph can change while the operator reads it. The changes probe is a
  // *notice*, never a merge: a new ledger version must be admitted by reloading
  // a fresh snapshot, because pages of two snapshots cannot be mixed. 实时 turns
  // that notice into an automatic whole-snapshot reload (docs/33 §4.6). The
  // probe asks `since=<snapshot seq>`, so run/message bookkeeping (which moves
  // the durable seq without a versions row) does not trigger a reload.
  const changesSince = useRef<number | undefined>(undefined)
  const changes = usePoll(
    () => {
      const since = graph.data?.snapshot_version?.seq
      changesSince.current = since
      // Before the first snapshot lands there is no seq to compare against; a
      // placeholder keeps the poll quiet instead of reading the whole journal.
      return since === undefined
        ? Promise.resolve<ChangePage>({
            changes: [],
            has_more: false,
            high_watermark: { epoch: '', seq: 0 },
            pending: 0,
          })
        : api.changes({ since })
    },
    6000,
    [graph.data?.snapshot_version?.seq],
  )
  // 实时 is on by default: the operator asked to see facts as they land. The
  // switch lets a reader freeze the map while inspecting a large or running
  // engagement; the snapshot contract itself does not change either way.
  const [live, setLive] = useSessionState('graph.live', true)
  const lastLiveReload = useRef(0)

  const [savedFilters, saveFilters] = useSessionState('graph.filters', { ...defaultGraphFilters(), hiddenKinds: [] as string[] })
  // A session saved before a switch existed must not read as "off": merge over the
  // defaults so old persisted filters gain the new drawing switches as on.
  const filters = useMemo<GraphFiltersState>(
    () => ({ ...defaultGraphFilters(), ...savedFilters, hiddenKinds: new Set(savedFilters.hiddenKinds) }),
    [savedFilters],
  )
  const setFilters = (next: GraphFiltersState | ((previous: GraphFiltersState) => GraphFiltersState)) => {
    const value = typeof next === 'function' ? next(filters) : next
    saveFilters({ ...value, hiddenKinds: [...value.hiddenKinds] })
  }
  const [q, setQ] = useSessionState('graph.search', '')
  const query = useDebounced(q, 200)
  const [showLabels, setShowLabels] = useState(false)
  // Layered is the reading view (facts → step → next fact, left to right); the
  // force map stays one click away for "what is connected to what".
  const [layout, setLayout] = useState<LayoutMode>(() => {
    try {
      return localStorage.getItem('tw.graph.layout') === 'force' ? 'force' : 'layered'
    } catch {
      return 'layered'
    }
  })
  // 下发 lives on the 图谱 page's right dock: intervention next to the map. The
  // full conversation record stays in the 通信 page.
  const [chatDock, setChatDock] = useState(false)
  // The inspector asks GraphPanel to open the child dialog for the direction the
  // operator is looking at; the dialog lives here because it belongs to the map,
  // not to the project shell.
  const [childParent, setChildParent] = useState<{ id: string; title: string } | null>(null)
  // The 终点 card is the goal's home on this page, so an unmet goal can be
  // redefined here instead of sending the operator back to 概览. The dialog is
  // the same one that page uses: it writes through the operator-scoped goal API
  // (a change to a met goal withdraws its receipt — the dialog says so).
  const [editingGoal, setEditingGoal] = useState<EditableGoal | null>(null)
  // Bumped after a child is created so the inspector's (otherwise static) child
  // list reloads without waiting for the operator to reselect the node.
  const [childRefresh, setChildRefresh] = useState(0)
  const [listView, setListView] = useState(false)
  const [focusOverlay, setFocusOverlay] = useState(false)
  // Time axis. The cursor is ephemeral on purpose: opening the map means "show me
  // the whole thing", and a half-scrubbed map that survived a reload would hide
  // facts for no stated reason. `null` = latest = everything revealed.
  const [timeCursor, setTimeCursor] = useState<number | null>(null)
  const [timelinePlaying, setTimelinePlaying] = useState(false)
  // Chain view. `pathSet` is the main path captured when the operator chose
  // 只看主路径; the model hook filters on it. `frozen` keeps the strip's numbers
  // from shrinking with its own filtered model.
  const [pathSet, setPathSet] = useState<ReadonlySet<string> | null>(null)
  const [chainFrozen, setChainFrozen] = useState<{ total: number; pathLength: number; coverage: number } | null>(null)
  // True once the operator picked a view for this selection; the automatic
  // big-closure default must not override that choice.
  const [chainChosen, setChainChosen] = useState(false)
  const pathMode = filters.focusOnly && pathSet !== null

  const chooseLayout = (next: LayoutMode) => {
    setLayout(next)
    try {
      localStorage.setItem('tw.graph.layout', next)
    } catch {
      /* private mode: the choice just does not persist */
    }
  }

  const defaultModel = connection.data?.model ?? ''

  const model = useGraphModel(
    { data: graph.data, project, runs: runs.data ?? [], defaultModel, selected, focusSet: pathMode ? pathSet : null },
    filters,
  )

  useEffect(() => {
    onError(graph.error || runs.error)
  }, [graph.error, runs.error, onError])

  /**
   * The selected node's chain, straight from model.ts (mainPath / chainClosure):
   * this file owns no chain rule of its own. Computed once per selection, never
   * per frame.
   */
  const chain = useMemo(() => {
    if (!selected) return null
    if (model.edgesById.has(selected)) return null
    const picked = model.byId.get(selected)
    if (!picked || picked.type === 'boundary') return null
    const closure = chainClosure(model, selected)
    const path = mainPath(model, selected)
    if (!closure || !path) return null
    const all = [closure.seed, ...closure.ancestors, ...closure.descendants]
    return {
      total: all.length,
      pathLength: path.length,
      coverage: model.nodes.length,
      pathSet: new Set(path),
      litSet: new Set(all.slice(0, CHAIN_CAP)),
    }
  }, [model, selected])

  // A chain belongs to one node: changing the selection leaves path mode, and
  // the numbers keep the snapshot taken when the mode was chosen (the filtered
  // model would otherwise report a shrinking chain).
  useEffect(() => {
    setPathSet(null)
    setChainFrozen(null)
    setChainChosen(false)
    setFilters((current) => (current.focusOnly ? { ...current, focusOnly: false } : current))
    // `setFilters` re-wraps the same filter object every render; selection is the
    // only trigger here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected])

  const chainView = chainFrozen ?? (chain ? { total: chain.total, pathLength: chain.pathLength, coverage: chain.coverage } : null)
  const selectionChain = pathMode ? pathSet : (chain?.litSet ?? null)

  // No automatic re-frame when the mode changes. `fit()` frames the whole canvas,
  // which includes the area under the node panel, and on a narrow map a five-hop
  // path fits only by zooming out past legibility. The toolbar's 适配 is the
  // operator's call; the strip's counts say what the view holds.

  const showChainClosure = () => {
    setPathSet(null)
    setChainFrozen(null)
    setChainChosen(true)
    setFilters((current) => ({ ...current, focusOnly: false }))
  }
  const showChainPath = () => {
    if (!chain) return
    setPathSet(chain.pathSet)
    setChainFrozen({ total: chain.total, pathLength: chain.pathLength, coverage: chain.coverage })
    setChainChosen(true)
    setFilters((current) => ({ ...current, focusOnly: true }))
  }

  // The brief's closure is almost the whole ledger (463 of 467 in the sampled
  // project): opening it as a closure would light 200 nodes and answer nothing,
  // so a closure past the cap opens on the main path instead. The strip still
  // offers 看完整, and that choice sticks for this selection.
  useEffect(() => {
    if (!chain || chainChosen) return
    if (defaultChainMode(chain.total) !== 'path') return
    setPathSet(chain.pathSet)
    setChainFrozen({ total: chain.total, pathLength: chain.pathLength, coverage: chain.coverage })
    setFilters((current) => (current.focusOnly ? current : { ...current, focusOnly: true }))
    // Runs once per selection model; while path mode is on the frozen snapshot
    // keeps it from firing again on the filtered model.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chain, chainChosen])

  // Deep link: a hash naming a node that is not in the snapshot (yet) asks the
  // snapshot for the neighborhood around it. The shell owns the hash; this is
  // just the read side that turns it into a focused page. An edge selection
  // names a panel subject, not a snapshot root: `graphSelectionRoot` keeps it
  // from re-rooting the map at a nonexistent `fact:…->s:…` record.
  useEffect(() => {
    if (!graph.data || graph.loading || !selected) return
    const root = graphSelectionRoot(selected)
    if (root && graph.root !== root && !model.byId.has(selected)) void graph.focus(root)
  }, [graph.data, graph.loading, graph.root, selected, model])

  // The three ledger polls that shaped the old graph are now one snapshot, so
  // "settled" is simply "the snapshot request finished". Settled (not "has
  // data") on purpose: a failed load must still release the camera instead of
  // leaving the map parked at the origin.
  const graphReady = !graph.loading

  const matches = useMemo(() => {
    if (!query.trim()) return null
    const needle = query.trim().toLowerCase()
    const set = new Set<string>()
    for (const n of model.nodes) {
      if (n.label.toLowerCase().includes(needle) || (n.sub ?? '').toLowerCase().includes(needle)) {
        set.add(n.id)
      }
    }
    for (const e of model.edges) {
      if (e.label?.toLowerCase().includes(needle)) set.add(e.id)
    }
    return set
  }, [query, model])

  // An unmet goal has no completed work to stand on, so it is not a map node
  // and owns no edge: the compact 终点 card states the target instead. A met
  // goal becomes a real node attached to its evidence fact (buildGraph).
  //
  // The snapshot keeps goals last and a read can stop short of them (a failed
  // page, or a snapshot the page budget could not finish). The card is therefore
  // not allowed to depend on that page set alone: the board always carries the
  // project's open goals, and is the fallback when the goal was not in this
  // snapshot.
  const openGoals = useMemo(() => {
    const loaded = (graph.data?.goals ?? []).filter((g) => !g.closed && !g.met)
    if (loaded.length > 0) return loaded
    return board?.goals?.open ?? []
  }, [graph.data, board])

  // Selection lookup goes through the model indexes (T2): no linear scans on a
  // 5k-node graph, and an edge id resolves to the direction it stands for.
  const selectedEdge = selected ? model.edgesById.get(selected) : undefined
  const selectedStep = selectedEdge?.intent?.id
    ? model.stepByIntent.get(selectedEdge.intent.id)
    : undefined
  const node: GraphNode | null = selectedEdge?.intent
    ? {
        id: selectedEdge.id,
        type: 'intent',
        label: selectedEdge.intent.title,
        sub: selectedEdge.intent.description,
        status: selectedEdge.intent.status,
        x: 0,
        y: 0,
        vx: 0,
        vy: 0,
        degree: 0,
        w: 0,
        h: 0,
        depth: 1,
        model: selectedStep?.model,
        // Provenance and helper decorations live on the step; carry them so an
        // edge selection opens the same panel a node selection does.
        origin: selectedStep?.origin,
        originIntent: selectedStep?.originIntent,
        lineageDepth: selectedStep?.lineageDepth,
        helpers: selectedStep?.helpers,
        meta: { intent: selectedEdge.intent },
      }
    : selected
      ? (model.byId.get(selected) ?? null)
      : null

  /** The intent a selection names, for the focus overlay. Edges and step/goal
   *  nodes all resolve to the direction whose work they describe. */
  const selectedIntentID = useMemo(() => {
    if (!selected) return null
    // Resolve an edge id before the `s:` prefix: a step→fact edge id also
    // starts with `s:` and would otherwise name a bogus intent.
    const edge = model.edgesById.get(selected)
    if (edge?.intent?.id) return edge.intent.id
    if (selected.startsWith('s:')) return selected.slice(2)
    return model.byId.get(selected)?.meta?.intent?.id ?? null
  }, [selected, model])

  const focusLayer = useFocusLayer(selectedIntentID, focusOverlay && Boolean(selectedIntentID))

  // The axis is a pure function of what is drawn: same model, same order. It is
  // built from the model (not the raw snapshot) so its counts describe the map on
  // screen, including a chain-only view.
  // Built from the snapshot, not the drawn model: the single-chain view and the
  // kind chips change what is drawn, never how many rows the axis has.
  const timeline = useMemo(() => buildTimelineFromSnapshot(graph.data ?? {}), [graph.data])
  const timelineUsable = timeline.order.length > 1
  // A shorter snapshot (chain-only view, a dropped fact) can leave the cursor past
  // the end: fall back to "latest" instead of pinning the map to a dead row.
  useEffect(() => {
    if (timeCursor !== null && timeCursor > timeline.order.length - 1) {
      setTimeCursor(null)
      setTimelinePlaying(false)
    }
  }, [timeCursor, timeline.order.length])

  // Playback: one row per step, the whole axis in 60 s, no speed control. The
  // map paints from the axis rank, so a tick is a cursor change, never a scan.
  useEffect(() => {
    if (!timelinePlaying) return
    const last = timeline.order.length - 1
    if (last <= 0) {
      setTimelinePlaying(false)
      return
    }
    const from = timeCursor ?? 0
    const remaining = Math.max(1, last - from)
    const duration = (remaining / last) * TIMELINE_PLAY_MS
    const started = performance.now()
    const timer = window.setInterval(() => {
      const progress = Math.min(1, (performance.now() - started) / duration)
      const next = from + Math.round(remaining * progress)
      setTimeCursor(next >= last ? null : next)
      if (progress >= 1) setTimelinePlaying(false)
    }, 100)
    return () => window.clearInterval(timer)
    // `timeCursor` is the start row, captured once by the click; re-running on
    // every tick would restart the clock.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [timelinePlaying, timeline.order.length])

  const timeGate = useMemo(
    () => (timelineUsable && timeCursor !== null ? { rank: timeline.rank, cursor: timeCursor } : null),
    [timeline, timelineUsable, timeCursor],
  )
  const timelineRevealed = !timelineUsable
    ? 0
    : timeCursor === null
      ? timeline.order.length
      : revealedCount(timeline, timeCursor)
  const timelineAt = timeline.order.length
    ? timeline.times[Math.min(timeline.order.length - 1, timeCursor ?? timeline.order.length - 1)]
    : ''

  const toggleTimeline = () => {
    if (timelinePlaying) {
      setTimelinePlaying(false)
      return
    }
    if (!timelineUsable) return
    // Play from the cursor, or replay the whole axis when it was already at the end.
    if (timeCursor === null) setTimeCursor(0)
    setTimelinePlaying(true)
  }

  // "Only the current chain" cannot outlive its anchor: clearing the selection
  // restores the full drawing instead of leaving a checked-but-inert switch.
  useEffect(() => {
    if (!selected && filters.focusOnly) setFilters((current) => ({ ...current, focusOnly: false }))
  }, [selected, filters.focusOnly])

  // Hover pulse for the inspector: a running direction shows idle time/context
  // pressure while the pointer rests on it.
  const pulseFor = useCallback(
    (intentID: string) => board?.running_intents.find((item) => item.id === intentID)?.pulse,
    [board],
  )

  // A snapshot page can arrive partial (a cut read, an older server). Counting
  // the rows it did carry would understate the map without saying so, and
  // `.length` on a missing list used to throw; the counter says 未读全 instead.
  const loadedNodes = graph.data
    && Array.isArray(graph.data.facts)
    && Array.isArray(graph.data.intents)
    && Array.isArray(graph.data.goals)
    ? graph.data.facts.length + graph.data.intents.length + graph.data.goals.length
    : undefined
  const totalNodes = graph.data?.total ?? 0
  const snapshotVersion = graph.data?.snapshot_version
  const watermark = changes.data?.high_watermark
  // A stale probe response from an older snapshot cannot speak for the one on
  // screen: only count its pending rows when it was asked with this snapshot's
  // seq, so switching snapshots does not replay an old "new changes" verdict.
  const probeMatchesSnapshot =
    snapshotVersion !== undefined &&
    changesSince.current !== undefined &&
    changesSince.current === snapshotVersion.seq
  const hasNewChanges = hasNewContent(
    snapshotVersion,
    watermark,
    probeMatchesSnapshot ? (changes.data?.pending ?? 0) : 0,
  )

  // 实时: a newer ledger watermark with content rows swaps in a whole new
  // snapshot; it never merges pages of two versions and never touches the
  // operator's camera. The throttle keeps a busy run from re-reading the whole
  // ledger on every poll.
  useEffect(() => {
    if (
      !shouldLiveReload({
        live,
        hidden: document.visibilityState === 'hidden',
        loading: graph.loading,
        hasNewChanges,
      })
    ) {
      return
    }
    const timer = window.setTimeout(() => {
      if (document.visibilityState === 'hidden') return
      lastLiveReload.current = Date.now()
      graph.reload()
    }, liveReloadDelay(Date.now(), lastLiveReload.current))
    return () => window.clearTimeout(timer)
  }, [live, graph.loading, graph.reload, hasNewChanges])

  function refresh() {
    graph.reload()
    onRefresh()
  }

  function handleCanvasSelect(id: string | null) {
    // Opening a node means "show me this", so the chat dock yields the right
    // side instead of sitting on top of the inspector.
    setChatDock(false)
    if (!id) {
      onSelect(null)
      return
    }
    // A boundary node is a not-yet-loaded reference: ask the snapshot for its
    // neighborhood instead of pinning a node the model does not have.
    const picked = model.byId.get(id)
    if (picked?.boundary) {
      void graph.focus(picked.boundary)
      return
    }
    onSelect(id)
  }

  const running = board?.intents.running ?? 0

  return (
    <Page
      title="图谱"
      scroll={false}
      className={`relative tw-graph-stage${timelineUsable ? ' has-timeline' : ''}`}
      meta={
        <>
          {/* Literal prefix kept: the acceptance script waits for 快照已载. */}
          <span
            className="inline-flex h-6 items-center gap-1 whitespace-nowrap text-[11px] text-ink-3"
            title={`项目 ID：${current}。斜杠后是账本里的节点总数。`}
          >
            快照已载
            <span className="tw-nums font-mono font-semibold text-ink-2">
              {loadedNodes === undefined ? '节点数未读全' : `${loadedNodes}/${totalNodes}`}
            </span>
            节点
          </span>
          <span className="h-3 w-px shrink-0 bg-hairline" aria-hidden />
          <Button
            size="xs"
            variant="ghost"
            className="gap-1.5"
            aria-pressed={live}
            onClick={() => setLive((value) => !value)}
            title={
              live
                ? '实时刷新已开：账本出现新变化时自动重取一份完整新快照（相机保留，载入进度不丢）'
                : '实时刷新已关：有新变化时只提示，手动点刷新才换快照'
            }
          >
            <span
              className={`h-1.5 w-1.5 shrink-0 rounded-full ${live ? 'motion-safe:animate-pulse bg-success' : 'bg-ink-3'}`}
            />
            实时
          </Button>
          {!live && hasNewChanges && (
            <Button
              size="xs"
              variant="soft"
              onClick={refresh}
              title="账本已有新变化。刷新会重取一份新快照；旧快照的页面不会被静默拼接。"
            >
              有新变化，点击刷新
            </Button>
          )}
          {graph.root && (
            <Button
              size="xs"
              variant="ghost"
              onClick={() => {
                onSelect(null)
                void graph.focus('')
              }}
            >
              全图概览
            </Button>
          )}
          <span className="hidden h-3 w-px shrink-0 bg-hairline sm:block" aria-hidden />
          {/* The one place the project token total lives now (docs/34 §3): the
              whole project, including cached input, reported by the model. */}
          <ProjectTokens usage={board?.usage} />
        </>
      }
      actions={
        <>
          <a
            className={`${buttonVariants({ variant: 'outline', size: 'sm' })} gap-1.5`}
            href={`/p/${encodeURIComponent(current)}/api/graph/export`}
            download
            title="导出账本里的全图（不是当前快照页）"
          >
            <Download className="h-4 w-4" />
            导出全图
          </a>
          <Button size="sm" variant="outline" className="gap-1.5" onClick={refresh} title="立即重取最新快照">
            <RefreshCw className="h-4 w-4" />
            刷新
          </Button>
        </>
      }
    >
      {/* The canvas is the first and only full-bleed child of the page body. At
          390px the other cards wrap around it instead of taking its place. */}
      <GraphCanvas
        ref={canvas}
        model={model}
        selected={selected}
        matches={matches}
        showLabels={showLabels}
        layout={layout}
        // One camera per project: a reload or a visit to another panel comes
        // back to the same pan/zoom instead of the opening centre.
        cameraKey={cameraStorageKey(current)}
        ready={graphReady}
        focusLayer={focusLayer}
        showOriginLinks={filters.showOriginLinks}
        showHelpers={filters.showHelpers}
        timeGate={timeGate}
        selectionChain={selectionChain}
        onSelect={handleCanvasSelect}
      />

      <GraphFilters
        current={current}
        q={q}
        onQueryChange={setQ}
        matchCount={matches ? matches.size : null}
        counts={board?.facts.by_kind ?? {}}
        filters={filters}
        onFiltersChange={setFilters}
        onFocusFact={(factID) => {
          // Whole-ledger search re-roots the snapshot at the fact, keeping the
          // historical behaviour: it does not pin a selection.
          void graph.focus(`fact:${factID}`)
        }}
        hasSelection={Boolean(selected)}
      />

      {chainView && !pathMode && (
        <GraphChainStrip
          total={chainView.total}
          coverage={chainView.coverage}
          pathLength={chainView.pathLength}
          shown={Math.min(chainView.total, CHAIN_CAP)}
          mode="closure"
          onMode={(mode) => (mode === 'path' ? showChainPath() : showChainClosure())}
          onClear={() => onSelect(null)}
        />
      )}
      {chainView && pathMode && (
        <GraphChainStrip
          total={chainView.total}
          coverage={chainView.coverage}
          pathLength={chainView.pathLength}
          shown={chainView.total}
          mode="path"
          onMode={(mode) => (mode === 'path' ? showChainPath() : showChainClosure())}
          onClear={() => onSelect(null)}
        />
      )}

      <GraphTimeline
        total={timeline.order.length}
        cursor={timeCursor}
        revealed={timelineRevealed}
        pending={timeline.order.length - timelineRevealed}
        untimed={timeline.untimed.length}
        atLabel={timelineAt ? fmtTime(timelineAt) : '—'}
        onCursor={(next) => {
          // Scrubbing is a deliberate stop: leaving playback running would fight
          // the pointer for the same cursor.
          setTimelinePlaying(false)
          setTimeCursor(next)
        }}
        playing={timelinePlaying}
        onTogglePlay={toggleTimeline}
        disabledReason={
          timeline.order.length === 1
            ? '只有一条带入账时间的记录，没有可回放的顺序。'
            : '这个快照里没有可排序的入账时间，所有节点按当前顺序显示。'
        }
      />

      <GraphToolbar
        chatDock={chatDock}
        onToggleChatDock={() => {
          setChatDock((open) => !open)
          onSelect(null)
        }}
        layout={layout}
        onLayout={chooseLayout}
        showLabels={showLabels}
        onToggleLabels={() => setShowLabels((value) => !value)}
        listView={listView}
        onToggleListView={() => setListView((value) => !value)}
        focusOverlay={focusOverlay}
        focusOverlayAvailable={Boolean(selectedIntentID)}
        onToggleFocusOverlay={() => setFocusOverlay((value) => !value)}
        onZoom={(factor) => canvas.current?.zoom(factor)}
        onFit={() => canvas.current?.fit()}
        onRelayout={() => canvas.current?.relayout()}
      />

      {/* legend + live commander card: the bottom-left corner is "what does the
          colour mean" plus the commander's current stance. An unmet goal is not
          connected to the chain, so it is a compact 终点 card here instead of a
          guessed node.
          The stack is bottom-anchored, so it can only grow upward, and the graph
          body clips its overflow: `tw-graph-stack` bounds it to the map box and
          the legend is the one child allowed to shrink — its own title row (the
          collapse control) stays visible while its body scrolls. Without that, an
          expanded legend grew past the map's top edge (under the banner) and its
          收起 button became unreachable. */}
      <div className="pointer-events-none tw-graph-corner-bl tw-graph-dock tw-graph-stack absolute flex flex-col gap-2">
        {openGoals.length > 0 && (
          <div className="pointer-events-auto max-w-[292px] shrink-0 rounded-xl border border-hairline bg-card/90 px-3 py-2.5 shadow-lifted backdrop-blur">
            <div className="flex items-center gap-1.5">
              <Crosshair className="h-3.5 w-3.5 shrink-0 text-warning" />
              <span className="text-[12px] font-semibold text-ink">
                终点 · 未达成{openGoals.length > 1 ? `（${openGoals.length}）` : ''}
              </span>
            </div>
            <div className="mt-1.5 space-y-1">
              {openGoals.slice(0, 2).map((g) => (
                <div key={g.id} className="space-y-0.5">
                  <div className="flex items-start gap-1.5">
                    <div className="tw-break line-clamp-2 min-w-0 flex-1 text-[12px] leading-relaxed text-ink-2" title={g.statement}>
                      {g.statement}
                    </div>
                    <Button
                      size="xs"
                      variant="ghost"
                      className="shrink-0 px-1 text-[11px] text-ink-3 hover:text-ink"
                      onClick={() => setEditingGoal(g)}
                      title="修改这个目标的起点/终点（会写回账本，已达成目标会撤回其达成收据）"
                    >
                      管理
                    </Button>
                  </div>
                  {g.start && (
                    <div className="line-clamp-1 text-[11px] leading-relaxed text-ink-3" title={g.start}>
                      起点：{g.start}
                    </div>
                  )}
                </div>
              ))}
            </div>
            <div className="mt-1.5 text-[11px] leading-relaxed text-ink-3">
              未完成前不画进链路，也不连线；达成后接到证据事实上
            </div>
          </div>
        )}

        <GraphLegend />

        <CommanderCard board={board} onOpen={() => onNavigate('decisions')} />
      </div>

      {/* live */}
      <div className="tw-graph-corner-br absolute flex items-center gap-1.5 rounded-full border border-hairline bg-card/90 px-3 py-1.5 text-[11px] text-ink-3 shadow-soft backdrop-blur">
        <span
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${
            running > 0 ? 'motion-safe:animate-pulse bg-primary' : live ? 'bg-success' : 'bg-ink-3'
          }`}
        />
        <span>
          {running > 0
            ? `${running} 个意图在跑`
            : live
              ? hasNewChanges
                ? '检测到新变化，正在重取快照…'
                : '实时 · 每 6 秒检测新变化'
              : '快照视图 · 有新变化时提示刷新'}
        </span>
        {running > 0 && (
          <>
            <span className="text-ink-3/50" aria-hidden>
              ·
            </span>
            <span>{live ? '实时' : '快照'}</span>
          </>
        )}
      </div>

      {/* Minimap: its own canvas (the acceptance script finds the main canvas by
          data-testid, not by "first canvas"). It shows the overview density, the
          directions still worth acting on and the current viewport; hidden while
          a right-side panel owns that corner. */}
      {!node && !chatDock && !listView && (
        <div className="tw-graph-minimap">
          <GraphMinimap
            model={model}
            live={running > 0 || model.nodes.some((n) => n.status === 'reviewing')}
            matches={matches}
            onCenter={(x, y) => canvas.current?.centerOn(x, y)}
          />
        </div>
      )}

      {!chatDock && node && (
        <Suspense fallback={null}>
          <GraphNodePanel
            node={node}
            facts={graph.data?.facts ?? []}
            runs={runs.data ?? []}
            dock={dock}
            onSelectFact={(id) => {
              if (!model.byId.get(`f:${id}`)?.meta?.fact) void graph.focus(`fact:${id}`)
              onSelect(`f:${id}`)
            }}
            onClose={() => onSelect(null)}
            onChanged={refresh}
            childRefresh={childRefresh}
            onChildIntent={(parent) => setChildParent(parent)}
            chain={chainView ? { total: chainView.total, pathLength: chainView.pathLength } : null}
            chainMode={pathMode ? 'path' : 'closure'}
            onChainMode={(mode) => (mode === 'path' ? showChainPath() : showChainClosure())}
          />
        </Suspense>
      )}

      {/* Hover is published by the canvas (T5's view-store) and subscribed only
          by this inspector: moving the pointer no longer re-renders GraphPanel.
          When a node is pinned the right side belongs to its full NodePanel, so
          hovering other nodes must not cover it with a second card. */}
      {!chatDock && !listView && !node && (
        <GraphHoverInspector model={model} selected={selected} pulseFor={pulseFor} width={dock.width} />
      )}

      {chatDock && (
        <GraphChatDock
          dock={dock}
          onClose={() => setChatDock(false)}
          onOpenConversation={() => {
            setChatDock(false)
            onNavigate('messages')
          }}
        />
      )}

      {/* List view is the accessible equivalent of the canvas. It overlays the
          canvas (its own root is absolute inset-0) instead of replacing it, so
          the camera/simulation survive the toggle and Esc can return focus to
          the still-mounted main canvas. */}
      {listView && (
        <GraphListView
          model={model}
          selected={selected}
          matches={matches}
          onSelect={handleCanvasSelect}
          onClose={() => setListView(false)}
        />
      )}

      {/* First open: the whole ledger is being read, so the map is empty by
          definition. Say that instead of flashing the "empty project" card,
          which is only true once a completed read found nothing. */}
      {graph.loading && !graph.data && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
          <div
            role="status"
            aria-live="polite"
            className="rounded-full border border-hairline bg-card/90 px-3.5 py-1.5 text-[11px] text-ink-3 shadow-soft backdrop-blur"
          >
            正在载入全图快照…
          </div>
        </div>
      )}

      {!graph.loading && !graph.error && !model.nodes.some((n) => n.type !== 'project') && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
          <div className="pointer-events-auto tw-panel w-[340px] rounded-xl">
            <EmptyState
              icon={Crosshair}
              title="这个项目还是空的"
              description="点「开始运行」让指挥官按项目简报推进并派发方向；目标由你在项目创建或「新建目标」里定义，简报和内置约束可在「概览」里补改。"
              action={
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => onNavigate('overview')}>
                    打开概览
                  </Button>
                  <Button size="sm" className="gap-1.5" onClick={onCreateIntent}>
                    <Plus className="h-4 w-4" />
                    新建意图
                  </Button>
                </div>
              }
            />
          </div>
        </div>
      )}

      {model.nodes.some((n) => n.type !== 'project') && (graph.data?.facts ?? []).length === 0 && (
        <div className="pointer-events-none absolute bottom-16 left-1/2 -translate-x-1/2 px-3">
          <div className="rounded-full border border-hairline bg-card/90 px-3.5 py-1.5 text-center text-[11px] text-ink-3 shadow-soft backdrop-blur">
            执行者还没有交接事实：写 `.tw/facts.jsonl` 后，事实会以圆点出现在意图周围
          </div>
        </div>
      )}

      {/* Goal editor opened from the 终点 card. Saving re-reads the snapshot and
          the board so the card and the map agree with the new definition. */}
      <GoalEditorDialog
        goal={editingGoal}
        onClose={() => setEditingGoal(null)}
        onSaved={() => {
          setEditingGoal(null)
          refresh()
        }}
      />

      {/* Child dialog: rendered here, not in the shell, because it is opened from
          the node inspector this panel owns. */}
      {childParent && (
        <CreateChildIntentDialog
          open
          onOpenChange={(v) => {
            if (!v) setChildParent(null)
          }}
          parent={childParent}
          onCreated={() => {
            setChildRefresh((n) => n + 1)
            refresh()
          }}
        />
      )}
    </Page>
  )
}
