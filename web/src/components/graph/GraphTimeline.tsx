// The graph's time axis control (docs/33 §4.6): one footer strip inside the map.
//
// What it answers is "when did this happen", and nothing else: it reveals the
// ledger in `created_at` order and never re-ranks, filters or colours anything.
// The strip is a footer rather than a floating card for one reason: it must stay
// visible while the map, the legend and the minimap are all in use, and a
// floating card in the bottom centre collides with the legend on a narrow map.
//
// Undated nodes are never hidden: the axis only conceals rows it can put in
// order. Their count sits in the strip so a scrub never looks complete.

import { Pause, Play, SkipForward } from 'lucide-react'
import { Button } from '@cyber/ui'

export interface GraphTimelineProps {
  /** Timed rows on the axis; 0 = nothing to order. */
  total: number
  /** Row index currently revealed; null = "latest" (all rows). */
  cursor: number | null
  /** Rows the axis has revealed, including the cursor row. */
  revealed: number
  /** Timed rows not revealed yet. */
  pending: number
  /** Drawn nodes with no usable timestamp: always visible, counted here. */
  untimed: number
  /** Clock label of the cursor row (or of the last row when cursor is null). */
  atLabel: string
  onCursor: (cursor: number | null) => void
  playing: boolean
  onTogglePlay: () => void
  /** Why the axis cannot move (no timestamps, or a single row). */
  disabledReason?: string
}

export function GraphTimeline({
  total,
  cursor,
  revealed,
  pending,
  untimed,
  atLabel,
  onCursor,
  playing,
  onTogglePlay,
  disabledReason,
}: GraphTimelineProps) {
  const last = Math.max(0, total - 1)
  const value = cursor ?? last
  const usable = total > 1
  return (
    <div
      className="tw-graph-timeline absolute inset-x-0 bottom-0 z-10 flex flex-col justify-center gap-1 border-t border-hairline bg-card/95 px-3 backdrop-blur"
      role="group"
      aria-label="时间轴：按入账顺序揭示节点"
      onKeyDown={(event) => {
        // Space toggles playback while the strip has focus; the range input keeps
        // its own arrow-key behaviour, so this never swallows a scrub.
        if (event.target !== event.currentTarget || event.key !== ' ') return
        if (!usable) return
        event.preventDefault()
        onTogglePlay()
      }}
    >
      <div className="mx-auto flex w-full max-w-3xl flex-wrap items-center gap-x-2.5 gap-y-1">
        <span className="text-[10.5px] uppercase tracking-wide text-ink-3">时间轴</span>
        {usable ? (
          <>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={playing ? '暂停' : '播放'}
              title={playing ? '暂停（空格）' : '按入账顺序播放（空格）'}
              onClick={onTogglePlay}
            >
              {playing ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
            </Button>
            <span className="tw-nums shrink-0 font-mono text-[11px] text-ink">{atLabel}</span>
            <input
              type="range"
              min={0}
              max={last}
              step={1}
              value={value}
              onChange={(event) => {
                const next = Number(event.target.value)
                onCursor(next >= last ? null : next)
              }}
              // The slider is the only scrub affordance: a pointer and a keyboard
              // reach the same rows, and the value text says where it landed.
              aria-label="按入账顺序揭示节点"
              aria-valuetext={`第 ${value + 1} 条，共 ${total} 条，${atLabel}`}
              className="h-4 min-w-[96px] flex-1 cursor-pointer accent-[var(--primary)]"
            />
            <Button
              size="xs"
              variant="ghost"
              className="shrink-0"
              disabled={cursor === null}
              title="回到最新：显示全部已入账节点"
              onClick={() => onCursor(null)}
            >
              <SkipForward className="h-3.5 w-3.5" /> 最新
            </Button>
            <span className="tw-nums shrink-0 text-[10.5px] text-ink-3" aria-live="polite">
              已揭示 {revealed}/{total}
              {pending > 0 && ` · 未到 ${pending}`}
              {untimed > 0 && ` · ${untimed} 无时间`}
            </span>
          </>
        ) : (
          <span className="text-[11px] text-ink-3">
            {disabledReason ?? '没有可排序的入账时间，所有节点按当前顺序显示。'}
            {untimed > 0 && `（${untimed} 个节点没有入账时间，始终显示）`}
          </span>
        )}
      </div>
    </div>
  )
}
