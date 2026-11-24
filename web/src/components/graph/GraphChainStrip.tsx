// 链路口径条（图谱画布顶部）：一次点击点亮的是什么，用一句话说清。
//
// 点节点之后图上是一条主干（编号 1、2、3……）加一跳旁支，其余压暗但仍可见；
// 这条条只报份数，跳表的每一行在右侧节点面板里。没有「隐藏成一条线」的模式了：
// 渗透链路是 DAG，砍成单线就丢掉「同一个事实还喂给别的意图」这条信息。
//
// 「只看这棵树」是一次显式动作：把画布换成全部祖先 + 自己 + 向下两跳，别的节点
// 不绘制。默认仍是全量，所以清除这个动作本身就能回到全图。

import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { Button } from '@cyber/ui'
import { GitBranch, Maximize2, X } from 'lucide-react'
import { closeAndFocusCanvas, isEscapeToClose } from './focus'

export interface GraphChainStripProps {
  /** 主干跳数（mainPath 上的节点数，编号到 hops 为止）。 */
  hops: number
  /** 一跳旁支的节点数（次亮，留在图上）。 */
  near: number
  /** 当前画布上的节点总数，用来说明这条链占了全图多少。 */
  coverage: number
  /** 树过滤开着：图上只剩这棵树。 */
  treeActive: boolean
  /** 这棵树的节点数（全部祖先 + 自己 + 向下两跳）。 */
  treeSize: number | null
  onOnlyTree: () => void
  onShowAll: () => void
  onClear: () => void
}

export function GraphChainStrip({
  hops,
  near,
  coverage,
  treeActive,
  treeSize,
  onOnlyTree,
  onShowAll,
  onClear,
}: GraphChainStripProps) {
  // 「清除选择（Esc）」要真的成立：焦点在这条条里时按 Esc 就清除，并把焦点交回画布
  // （这条条会随选择一起卸载，不交回去焦点就掉到 BODY）。
  function clearAndReturnFocus() {
    closeAndFocusCanvas(onClear)
  }

  function onKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (!isEscapeToClose(event)) return
    event.preventDefault()
    clearAndReturnFocus()
  }

  return (
    <div
      className="tw-graph-chainstrip pointer-events-auto absolute z-10 flex max-w-[min(92cqi,42rem)] flex-wrap items-center gap-x-2 gap-y-1 rounded-xl border border-hairline bg-card/95 px-2.5 py-1.5 text-[11.5px] shadow-lifted backdrop-blur"
      role="group"
      aria-label="选中节点的渗透链路"
      onKeyDown={onKeyDown}
    >
      <span className="tw-nums text-ink">
        主干 <b className="font-semibold">{hops}</b> 跳
      </span>
      <span className="text-ink-3">·</span>
      <span className="tw-nums text-ink-3" title="主干之外、一跳可达的节点：同一个事实还被哪些意图消费，这些意图又产出了什么">
        旁支 {near}
      </span>
      <span className="text-ink-3">·</span>
      <span className="tw-nums text-ink-3" title={treeActive ? '这棵树：全部祖先 + 这个节点 + 向下两跳，其余节点不绘制' : '当前画布上的节点数'}>
        {treeActive ? `这棵树 ${treeSize ?? 0}` : `全图 ${coverage}`}
      </span>
      <span className="mx-0.5 h-4 w-px bg-hairline" aria-hidden />
      {treeActive ? (
        <Button
          size="xs"
          variant="ghost"
          title="回到全图：把被这棵树挡掉的节点重新画出来（默认就是全量）"
          onClick={onShowAll}
        >
          <Maximize2 className="h-3.5 w-3.5" /> 看全图
        </Button>
      ) : (
        <Button
          size="xs"
          variant="ghost"
          title="只看这棵树：这个节点的全部祖先 + 它自己 + 向下两跳，别的节点先不画，方便顺着起点读过来"
          onClick={onOnlyTree}
        >
          <GitBranch className="h-3.5 w-3.5" /> 只看这棵树
        </Button>
      )}
      <Button size="icon-sm" variant="ghost" aria-label="清除选择" title="清除选择（Esc）" onClick={clearAndReturnFocus}>
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}
