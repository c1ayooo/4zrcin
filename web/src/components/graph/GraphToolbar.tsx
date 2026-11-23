// The graph canvas toolbar (T6, docs/33 §4.6): the draw controls plus the two
// switches (list view, intent labels). Buttons only report intent; the page owns
// the state and the canvas ref.
//
// Every control is an icon, as the operator asked: the row stays compact over the
// canvas. Each one therefore carries both an `aria-label` and a Chinese `title`,
// so the name is readable on hover and to assistive tech instead of being guessed.
// Layout and label switches live behind 视图, where each control has a visible word.

import {
  Clock,
  GitBranch,
  List,
  Maximize2,
  MessageSquare,
  Network,
  RotateCcw,
  SlidersHorizontal,
  Tags,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import {
  Button,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@cyber/ui'
import type { LayoutMode } from '@/graph/sim'
import { TIMELINE_BODY_ID } from './GraphTimeline'

export interface GraphToolbarProps {
  chatDock: boolean
  onToggleChatDock: () => void
  layout: LayoutMode
  onLayout: (layout: LayoutMode) => void
  showLabels: boolean
  onToggleLabels: () => void
  /** The accessible list equivalent of the canvas. */
  listView: boolean
  onToggleListView: () => void
  onZoom: (factor: number) => void
  onFit: () => void
  /** Discard manual node drops and restore the computed layout. */
  onRelayout: () => void
  /** The time axis strip at the map footer; off means it takes no height. */
  timelineOpen: boolean
  onToggleTimeline: () => void
  /** Revealed/total for the switch's tooltip, so the state is readable closed. */
  timelineCounts?: string
}

export function GraphToolbar({
  chatDock,
  onToggleChatDock,
  layout,
  onLayout,
  showLabels,
  onToggleLabels,
  listView,
  onToggleListView,
  onZoom,
  onFit,
  onRelayout,
  timelineOpen,
  onToggleTimeline,
  timelineCounts,
}: GraphToolbarProps) {
  const divider = <span className="mx-0.5 h-5 w-px shrink-0 bg-hairline" aria-hidden />
  return (
    <div
      className="tw-graph-corner-tr tw-graph-toolbar absolute flex gap-1 rounded-xl border border-hairline bg-card/90 p-1.5 shadow-lifted backdrop-blur"
      role="toolbar"
      aria-label="图谱视图控件"
    >
      <Button
        size="icon-sm"
        variant="ghost"
        active={chatDock}
        title="下发：打开通信页的下发入口（右侧面板）"
        aria-label="下发"
        onClick={onToggleChatDock}
      >
        <MessageSquare className="h-4 w-4" />
        {/* A word where there is room; the icon keeps the narrow corner. The
            bubble glyph alone does not say "dispatch" to someone who has never
            hovered it. */}
      </Button>
      {divider}
      <Button size="icon-sm" variant="ghost" title="放大" aria-label="放大" onClick={() => onZoom(1.25)}>
        <ZoomIn className="h-4 w-4" />
      </Button>
      <Button size="icon-sm" variant="ghost" title="缩小" aria-label="缩小" onClick={() => onZoom(0.8)}>
        <ZoomOut className="h-4 w-4" />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        title="适配画布（键盘：方向键移动选择 · Enter 打开 · Esc 取消）"
        aria-label="适配画布"
        onClick={onFit}
      >
        <Maximize2 className="h-4 w-4" />
      </Button>
      {divider}
      <Button
        size="icon-sm"
        variant="ghost"
        title="重新布局：按依赖重新排列，并清除手动拖动的节点位置"
        aria-label="重新布局"
        onClick={onRelayout}
      >
        <RotateCcw className="h-4 w-4" />
      </Button>
      {divider}
      {/* The time axis used to sit at the map footer even closed, taking a strip
          of every graph screen. Its switch now lives here, next to the layout
          controls it belongs to, and the footer only exists while open. */}
      <Button
        size="icon-sm"
        variant="ghost"
        active={timelineOpen}
        aria-expanded={timelineOpen}
        aria-controls={timelineOpen ? TIMELINE_BODY_ID : undefined}
        title={
          timelineOpen
            ? '时间轴已展开：点一下收起'
            : `时间轴：按入账顺序揭示节点${timelineCounts ? `（${timelineCounts}）` : ''}`
        }
        aria-label="时间轴"
        onClick={onToggleTimeline}
      >
        <Clock className="h-4 w-4" />
      </Button>
      <Popover>
        <PopoverTrigger asChild>
          <Button
            size="icon-sm"
            variant="ghost"
            title="视图：布局、标签与列表"
            aria-label="视图"
          >
            <SlidersHorizontal className="h-4 w-4" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-60 p-1.5">
          <div className="px-1.5 pb-1 pt-0.5 text-[10.5px] uppercase tracking-wide text-ink-3">布局</div>
          <div className="flex gap-1">
            <Button
              size="sm"
              variant="ghost"
              className="flex-1 justify-start gap-1.5"
              active={layout === 'layered'}
              title="链路树：起点 → 每一步意图 → 它产出的事实；目标达成后接到证据事实上（未达成的终点在左下终点卡）"
              onClick={() => onLayout('layered')}
            >
              <GitBranch className="h-3.5 w-3.5" /> 链路树
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="flex-1 justify-start gap-1.5"
              active={layout === 'force'}
              title="力导向：按连接关系铺开，看整体形状与离群节点；要读单条依赖顺序用链路树"
              onClick={() => onLayout('force')}
            >
              <Network className="h-3.5 w-3.5" /> 力导向
            </Button>
          </div>
          <p className="px-1.5 pb-1.5 pt-0.5 text-[10.5px] leading-4 text-ink-3">
            链路树按依赖顺序读一条链；力导向按连接关系铺开，看整体形状与离群节点。
          </p>

          <div className="border-t border-hairline" />
          <div className="px-1.5 pb-0.5 pt-1.5 text-[10.5px] uppercase tracking-wide text-ink-3">显示</div>
          <Button
            size="sm"
            variant="ghost"
            className="w-full justify-start gap-1.5"
            active={showLabels}
            aria-pressed={showLabels}
            title={showLabels ? '意图标签已显示：点一下隐藏' : '意图标签已隐藏：点一下显示'}
            onClick={onToggleLabels}
          >
            <Tags className="h-3.5 w-3.5" /> 意图标签
            <span className="ml-auto text-[11px] text-ink-3">{showLabels ? '开' : '关'}</span>
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="w-full justify-start gap-1.5"
            active={listView}
            title="列表视图（画布的可访问等价入口；键盘可达，Esc 关闭）"
            onClick={onToggleListView}
          >
            <List className="h-3.5 w-3.5" /> 列表视图
          </Button>
        </PopoverContent>
      </Popover>
    </div>
  )
}
