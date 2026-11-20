// The chain strip (docs/33 §4.6): what the selected node's chain is, and the two
// ways to look at it.
//
// It sits at the top centre of the map on a wide layout. On a narrow map the top
// corners already belong to the filters card and the toolbar, so the strip is
// hidden there and the same two actions live in the node panel, which is open
// whenever a node is selected. Rendering both would be two entries for one job.
//
// 看完整 lights the whole closure without hiding anything (the map keeps its
// context). 只看主路径 hides every node off the deepest-up / longest-down path:
// that is the single-chain view, and it is the only mode that changes the model.

import { Button } from '@cyber/ui'
import { GitBranch, Maximize2, X } from 'lucide-react'

export type ChainMode = 'closure' | 'path'

export interface GraphChainStripProps {
  /** Nodes in the full closure (seed + ancestors + descendants). */
  total: number
  /** Nodes drawn in the current model: the "of N" in the coverage figure. */
  coverage: number
  /** Nodes on the main path (deepest up + longest down). */
  pathLength: number
  /** Nodes the closure highlight actually lights when it is capped. */
  shown: number
  mode: ChainMode
  onMode: (mode: ChainMode) => void
  onClear: () => void
}

export function GraphChainStrip({
  total,
  coverage,
  pathLength,
  shown,
  mode,
  onMode,
  onClear,
}: GraphChainStripProps) {
  const capped = shown < total
  return (
    <div
      className="tw-graph-chainstrip pointer-events-auto absolute z-10 flex max-w-[min(92cqi,42rem)] flex-wrap items-center gap-x-2 gap-y-1 rounded-xl border border-hairline bg-card/95 px-2.5 py-1.5 text-[11.5px] shadow-lifted backdrop-blur"
      role="group"
      aria-label="选中节点的链路"
    >
      <span className="tw-nums text-ink">
        链路 <b className="font-semibold">{total}</b> 个节点
      </span>
      <span className="text-ink-3">·</span>
      <span className="tw-nums text-ink-3">
        覆盖 {total}/{coverage}
      </span>
      {capped && (
        <span className="tw-nums text-warning" title="闭包超过显示上限，只点亮离选中最近的节点">
          显示前 {shown}
        </span>
      )}
      <span className="mx-0.5 h-4 w-px bg-hairline" aria-hidden />
      <Button
        size="xs"
        variant="ghost"
        active={mode === 'closure'}
        title="看完整：点亮整条闭包，其余压暗；不隐藏任何节点"
        onClick={() => onMode('closure')}
      >
        <Maximize2 className="h-3.5 w-3.5" /> 看完整
      </Button>
      <Button
        size="xs"
        variant="ghost"
        active={mode === 'path'}
        title={`只看主路径：只保留最深上溯 + 最长下溯这一条（${pathLength} 个节点），其余隐藏`}
        onClick={() => onMode('path')}
      >
        <GitBranch className="h-3.5 w-3.5" /> 只看主路径
      </Button>
      <Button size="icon-sm" variant="ghost" aria-label="清除选择" title="清除选择（Esc）" onClick={onClear}>
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}
