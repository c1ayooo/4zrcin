// The graph canvas toolbar (T6, docs/33 §4.6): the draw controls plus the two
// switches (list view, focus overlay). Buttons only report intent; the page owns
// the state and the canvas ref.
//
// Discoverability: a touch screen has no hover, so an icon row that explains
// itself only through `title` is unusable there. Zoom / fit / send stay on the
// row (the moves an operator makes constantly); everything else lives behind one
// labelled "视图" popover, where each control has a visible word.

import {
  Focus,
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
  /** Focus overlay (hot/warm/cold) for the selected direction's chain. */
  focusOverlay: boolean
  /** False while no intent is selected: an overlay without a focus is noise. */
  focusOverlayAvailable: boolean
  onToggleFocusOverlay: () => void
  onZoom: (factor: number) => void
  onFit: () => void
  /** Discard manual node drops and restore the computed layout. */
  onRelayout: () => void
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
  focusOverlay,
  focusOverlayAvailable,
  onToggleFocusOverlay,
  onZoom,
  onFit,
  onRelayout,
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
      <Popover>
        <PopoverTrigger asChild>
          <Button size="icon-sm" variant="ghost" title="视图：布局、标签、列表与焦点叠层" aria-label="视图">
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
            读一条链用链路树；看整体形状、孤岛和离群点用力导向。
          </p>

          <div className="border-t border-hairline" />
          <div className="px-1.5 pb-0.5 pt-1.5 text-[10.5px] uppercase tracking-wide text-ink-3">显示</div>
          <Button
            size="sm"
            variant="ghost"
            className="w-full justify-start gap-1.5"
            active={showLabels}
            title={showLabels ? '隐藏意图标签' : '显示意图标签'}
            onClick={onToggleLabels}
          >
            <Tags className="h-3.5 w-3.5" /> {showLabels ? '隐藏意图标签' : '显示意图标签'}
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
          <Button
            size="sm"
            variant="ghost"
            className="w-full justify-start gap-1.5"
            active={focusOverlay}
            disabled={!focusOverlayAvailable}
            title="焦点叠层：按本次派发的焦点投影给链路分热/温/冷（hot 加环，cold 淡化）；只影响绘制"
            onClick={onToggleFocusOverlay}
          >
            <Focus className="h-3.5 w-3.5" /> 焦点叠层
          </Button>
          {!focusOverlayAvailable && (
            // The reason is a visible line, not a tooltip: touch screens have no
            // hover, and a disabled control with no reason reads as broken.
            <p className="px-1.5 pb-1 text-[10.5px] leading-4 text-ink-3">先选中一个方向，才有焦点链可分冷热。</p>
          )}

          <div className="border-t border-hairline" />
          <Button
            size="sm"
            variant="ghost"
            className="mt-1 w-full justify-start gap-1.5"
            title="重新布局：按依赖重新排列，并清除手动拖动的节点位置"
            onClick={onRelayout}
          >
            <RotateCcw className="h-3.5 w-3.5" /> 重新布局
          </Button>
        </PopoverContent>
      </Popover>
    </div>
  )
}
