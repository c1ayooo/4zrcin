// The graph page's search/filter card (T6, docs/33 §4.6). Two groups:
//  1) the historical card (retrieval mode, whole-ledger search, match list,
//     free-text node search, fact-kind chips, match count);
//  2) the drawing-only filters added by T7's lib/graph-filter.ts: recency
//     window over facts, hide finished branches, keep only the selected chain.
// None of these change what the ledger says; they change what is drawn.
//
// The node search stays visible on narrow maps; the rest opens from 筛选与检索.

import { useEffect, useId, useRef, useState } from 'react'
import type { Ref } from 'react'
import { ChevronDown, Search } from 'lucide-react'
import { Button, Chip, Input } from '@cyber/ui'
import { api } from '@/lib/api'
import { useLoad } from '@/lib/hooks'
import { GRAPH_TIME_RANGES } from '@/lib/graph-filter'
import type { Fact } from '@/lib/types'
import type { GraphFiltersState } from './use-graph-model'

const FACT_KINDS = ['vuln', 'cred', 'host', 'service', 'loot', 'artifact', 'note']

export interface GraphFiltersProps {
  /** Project id: the index build and the search are project-scoped. */
  current: string
  /** Debounced query the canvas highlights; owned by GraphPanel. */
  q: string
  onQueryChange: (q: string) => void
  /** Size of the current match set, or null when the query is empty. */
  matchCount: number | null
  /**
   * The matched nodes by name. The canvas only highlights them, so without this
   * a screen reader (and any automated check) cannot tell what was found.
   */
  matchNames?: readonly string[]
  /** Board fact counts per kind, for the chips. */
  counts: Record<string, number>
  filters: GraphFiltersState
  onFiltersChange: (next: GraphFiltersState) => void
  /** Jump the map to a fact found by the whole-ledger search. */
  onFocusFact: (factID: string) => void
  /** 冷热压暗开关：选中后把链路外的节点压暗（默认关，链路高亮与它各自独立）。 */
  coldDim: boolean
  onToggleColdDim: (next: boolean) => void
  /**
   * The card's root element. The map measures it: this card covers the canvas's
   * left edge, and `fit()` has to frame the tree into the strip the card leaves
   * visible (measured, never a hardcoded width).
   */
  cardRef?: Ref<HTMLDivElement>
}

export function GraphFilters({
  current,
  q,
  onQueryChange,
  matchCount,
  matchNames,
  counts,
  filters,
  onFiltersChange,
  onFocusFact,
  coldDim,
  onToggleColdDim,
  cardRef,
}: GraphFiltersProps) {
  const [showOptions, setShowOptions] = useState(false)
  const optionsID = useId()
  const [searchMode, setSearchMode] = useState('lexical')
  const [searchCursor, setSearchCursor] = useState('')
  const [searchTerm, setSearchTerm] = useState('')
  const [globalMatches, setGlobalMatches] = useState<Fact[]>([])
  const [searchingGraph, setSearchingGraph] = useState(false)
  const embedding = useLoad(api.retrievalStatus, [])
  const [indexing, setIndexing] = useState(false)
  const [indexProgress, setIndexProgress] = useState('')
  // Search/index failures are local to this card (the page-level action error
  // belongs to pause/decide); never swallow a rejected promise.
  const [searchError, setSearchError] = useState('')
  const indexLive = useRef(true)
  const indexGeneration = useRef(0)
  useEffect(() => {
    indexGeneration.current++
    indexLive.current = true
    setIndexing(false)
    setIndexProgress('')
    setSearchError('')
    setGlobalMatches([])
    setSearchCursor('')
    return () => {
      indexLive.current = false
      indexGeneration.current++
    }
  }, [current])

  async function buildIndex() {
    setIndexing(true)
    setSearchError('')
    let cursor = ''
    const generation = indexGeneration.current
    try {
      do {
        const page = await api.indexFacts(cursor)
        if (!indexLive.current || generation !== indexGeneration.current) return
        setIndexProgress(`尚待读取 ${page.remaining_to_visit}/${page.total}`)
        cursor = page.next_cursor ?? ''
        if (!page.has_more) break
      } while (cursor && indexLive.current && generation === indexGeneration.current)
      setIndexProgress('索引已就绪')
    } catch (e) {
      if (indexLive.current && generation === indexGeneration.current) {
        setSearchError(e instanceof Error ? e.message : String(e))
      }
    } finally {
      if (indexLive.current && generation === indexGeneration.current) setIndexing(false)
    }
  }

  async function searchGraph(cursor = '') {
    setSearchingGraph(true)
    setSearchError('')
    try {
      const term = cursor ? searchTerm : q
      const page = await api.factsPage({ q: term, mode: searchMode, limit: 20, cursor })
      setSearchTerm(term)
      setSearchCursor(page.next_cursor ?? '')
      setGlobalMatches((previous) => (cursor ? [...previous, ...page.facts] : page.facts))
    } catch (e) {
      setSearchError(e instanceof Error ? e.message : String(e))
    } finally {
      setSearchingGraph(false)
    }
  }

  function toggleKind(kind: string) {
    const next = new Set(filters.hiddenKinds)
    if (next.has(kind)) next.delete(kind)
    else next.add(kind)
    onFiltersChange({ ...filters, hiddenKinds: next })
  }

  const patch = (part: Partial<GraphFiltersState>) => onFiltersChange({ ...filters, ...part })

  // Collapsed controls must never hide that they are doing something: the
  // summary carries how many drawing filters are in effect.
  const activeFilters =
    filters.hiddenKinds.size +
    (filters.timeRange !== 'all' ? 1 : 0) +
    (filters.hideDone ? 1 : 0) +
    (filters.showOriginLinks === false ? 1 : 0) +
    (filters.showHelpers === false ? 1 : 0)

  return (
    <div ref={cardRef} className="pointer-events-none tw-graph-corner-tl tw-graph-filter absolute">
      <div className="pointer-events-auto tw-graph-overlay-scroll tw-graph-overlay-gap-10 overflow-y-auto rounded-xl border border-hairline bg-card/90 p-3 shadow-lifted backdrop-blur">
        {/* One 12px toolbar: search first, then the retrieval mode, then the
            drawing-only filters. Everything is the same face size so the card
            reads as a control strip, not a form. */}
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-3" />
          <Input
            value={q}
            onChange={(e) => onQueryChange(e.target.value)}
            placeholder="搜索节点"
            aria-label="搜索节点"
            className="h-8 border-hairline bg-surface-2/50 pl-8 text-[12px]"
          />
        </div>
        {matchCount !== null && (
          <div className="mt-1.5 text-[11px] text-ink-3">
            命中 <span className="tw-nums font-mono font-semibold text-ink-2">{matchCount}</span> 个节点
          </div>
        )}
        {/*
          The canvas can only highlight a match; it has no accessible name. This
          list is the same information in the DOM: a screen reader hears what was
          found, and an acceptance check has something to assert. Visually it is
          sr-only, so the default look does not change.
        */}
        {matchCount !== null && matchNames && matchNames.length > 0 && (
          <div className="sr-only" role="status" aria-live="polite" aria-label="搜索结果">
            <p>
              命中 {matchCount} 个节点：
              {matchNames.slice(0, 20).join('、')}
              {matchNames.length > 20 ? `，等 ${matchNames.length} 个` : ''}
            </p>
          </div>
        )}
        <Button
          size="xs"
          variant="ghost"
          className="tw-graph-filter-toggle mt-1 w-full justify-start gap-1.5 px-1"
          aria-expanded={showOptions}
          aria-controls={optionsID}
          onClick={() => setShowOptions(open => !open)}
        >
          <ChevronDown aria-hidden="true" className={`size-3 ${showOptions ? 'rotate-180' : ''}`} />
          筛选与检索{activeFilters > 0 && `（${activeFilters}）`}
        </Button>
        <div id={optionsID} className="tw-graph-filter-options mt-2" data-open={showOptions || undefined}>
          <div className="mb-2 flex flex-wrap gap-1">
            {FACT_KINDS.map((k) => (
              <Chip
                key={k}
                active={!filters.hiddenKinds.has(k)}
                onClick={() => toggleKind(k)}
                className="gap-1 px-2 py-0.5 text-[11px] leading-4"
                title={filters.hiddenKinds.has(k) ? `显示 ${k}` : `隐藏 ${k}`}
              >
                {k}
                <span className="tw-nums font-mono text-[11px] opacity-60">{counts[k] ?? 0}</span>
              </Chip>
            ))}
          </div>
          <div className="flex gap-1.5">
            <select
              className="h-8 min-w-0 flex-1 rounded-md border border-hairline bg-surface-2/50 px-2 text-[12px] text-ink-2 outline-none transition-colors hover:border-ink-3/40 focus-visible:border-ring/60 focus-visible:ring-2 focus-visible:ring-ring/35"
              value={searchMode}
              aria-label="检索模式"
              onChange={(e) => {
                setSearchMode(e.target.value)
                setSearchCursor('')
                setGlobalMatches([])
              }}
            >
              <option value="lexical">全文检索</option>
              <option value="semantic" disabled={!embedding.data?.configured}>
                语义检索
              </option>
              <option value="hybrid" disabled={!embedding.data?.configured}>
                混合检索
              </option>
            </select>
            {embedding.data?.configured && (
              <Button size="xs" variant="ghost" disabled={indexing} onClick={() => void buildIndex()}>
                构建索引
              </Button>
            )}
          </div>
          <div className="mt-1 flex items-center gap-1.5">
            <Button size="xs" variant="ghost" onClick={() => void searchGraph()} disabled={searchingGraph || !q.trim()}>
              全库检索
            </Button>
            <span className="text-[11px] text-ink-3">边界节点可点击展开</span>
          </div>
          {indexProgress && <div className="mt-1 text-[11px] text-ink-3">{indexProgress}</div>}
          {searchError && <div className="mt-1 text-[11px] text-destructive">{searchError}</div>}
          {globalMatches.map((f) => (
            <button
              key={f.id}
              className="mt-1 block w-full truncate rounded px-1.5 py-1 text-left text-[12px] text-info transition-colors hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
              onClick={() => {
                setGlobalMatches([])
                onQueryChange('')
                onFocusFact(f.id)
              }}
            >
              {f.title}
            </button>
          ))}
          {searchCursor && (
            <Button size="xs" variant="ghost" disabled={searchingGraph} onClick={() => void searchGraph(searchCursor)}>
              继续检索结果
            </Button>
          )}

          {/* Drawing-only filters. The ledger is never cut: these decide what the
              canvas draws (and therefore what the layout/hit-test sees). They are
              collapsed by default: finding something and choosing what to draw are
              two different questions, and the card answers the first one on open. */}
          <details className="mt-2.5 border-t border-hairline pt-2.5">
            <summary className="cursor-pointer select-none text-[11px] text-ink-3 transition-colors hover:text-ink">
              画什么 · 类型 / 时间 / 显示
              {activeFilters > 0 && <span className="ml-1 text-ink-2">· {activeFilters} 项生效</span>}
            </summary>
            <div className="space-y-2 pt-2">
            <div className="flex items-center gap-2">
              <span className="shrink-0 text-[11px] text-ink-3">时间</span>
              <select
                className="h-7 min-w-0 flex-1 rounded-md border border-hairline bg-surface-2/50 px-1.5 text-[11px] text-ink-2 outline-none transition-colors hover:border-ink-3/40 focus-visible:border-ring/60 focus-visible:ring-2 focus-visible:ring-ring/35"
                value={filters.timeRange}
                aria-label="时间范围（按事实创建时间）"
                title="只按事实创建时间过滤；没有时间戳的事实视为「时间未知」而保留"
                onChange={(e) => patch({ timeRange: e.target.value as GraphFiltersState['timeRange'] })}
              >
                {GRAPH_TIME_RANGES.map((range) => (
                  <option key={range.id} value={range.id}>
                    {range.label}
                  </option>
                ))}
              </select>
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-[12px] text-ink-2 transition-colors hover:text-ink">
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-primary"
                checked={filters.hideDone}
                onChange={(e) => patch({ hideDone: e.target.checked })}
              />
              隐藏已完成分支
            </label>
            {/* Two drawing-only switches for the second channel: the 发起者 marks
                stay (they are the second channel), only the derived 发起边 and the
                temporary-helper satellites can be hidden. */}
            <label
              className="flex cursor-pointer items-center gap-2 text-[12px] text-ink-2 transition-colors hover:text-ink"
              title="选中节点后把链路以外的节点压到几乎看不见（冷热分层压暗）。关着时链路照常高亮——主干边加粗、跳表逐跳标注，其余节点保持正常亮度"
            >
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-primary"
                checked={coldDim}
                onChange={(e) => onToggleColdDim(e.target.checked)}
              />
              冷热压暗
            </label>
            <label
              className="flex cursor-pointer items-center gap-2 text-[12px] text-ink-2 transition-colors hover:text-ink"
              title="执行者发起的平级意图连回发起者：1px 虚线 + 开口箭头，低透明度；不是账本边，不参与布局"
            >
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-primary"
                checked={filters.showOriginLinks !== false}
                onChange={(e) => patch({ showOriginLinks: e.target.checked })}
              />
              显示发起线
            </label>
            <label
              className="flex cursor-pointer items-center gap-2 text-[12px] text-ink-2 transition-colors hover:text-ink"
              title="步骤 pill 右上角的原生 subagent 卫星点与“助×N”（父回合内临时助手，不登记 run/usage、不产生事实）"
            >
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-primary"
                checked={filters.showHelpers !== false}
                onChange={(e) => patch({ showHelpers: e.target.checked })}
              />
              显示助手
            </label>
            </div>
          </details>
        </div>
      </div>
    </div>
  )
}
