// 项目横幅里的账本计数（意图 / 事实 / 证据 / 在跑 / 快照节点）。
//
// 两种形态，同一份数字口径：
//   - `BoardSummary`：图谱页的收纳态。触发器只写「账本」，展开是逐行明细，
//     形式与同一行的 tokens 用量一致——窄窗口也不隐藏、不缩字号。
//   - `BoardCounters`：其他页的平铺一行（意图 | 事实 | 证据 | 在跑）。
//
// 分隔线是 1px 竖线（14px 高、两侧各 10px），不再用字符 `|`——字符随字体缩放，和真实
// 分隔线对不齐。数值一律等宽 tabular，行内小数点对齐。
//
// 为什么不复用同一行的 `ProjectTokens`：那个是 hover/focus 展开的用量胶囊，面板里是
// 输入/输出明细；这里要的是点击展开、Esc 收起、能读屏的明细表。但**做法**照抄它：
// 内联绝对定位面板，不经 Portal——触屏实机上 Portal 弹层不显示（见 BoardSummary 注释）。
import { useEffect, useId, useRef, useState } from 'react'
import { ChevronDown } from 'lucide-react'
import type { BoardResponse } from '@/lib/types'
import { fmtInt } from '@/lib/format'

/** 状态线里的一条分隔线：1px 宽、14px 高、两侧各 10px。 */
export function MetaRule({ className = '' }: { className?: string }) {
  return <span className={`mx-2.5 h-3.5 w-px shrink-0 bg-hairline ${className}`} aria-hidden />
}

/** 与 tokens 触发器同一套尺寸与圆角：28px 行内 24px 高的小胶囊。 */
const TRIGGER =
  'group inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-sm font-mono text-[11px] text-ink-3 outline-none transition-colors hover:text-ink focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background'

/** 一行明细：标签左、数值右（等宽，右对齐）。 */
function Row({
  label,
  value,
  tone,
  dot,
}: {
  label: string
  value: string
  tone?: string
  dot?: boolean
}) {
  return (
    <>
      <span className="flex items-center gap-1.5 text-ink-3">
        {dot && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-primary" aria-hidden />}
        {label}
      </span>
      <span className={`text-right font-mono tabular-nums ${tone ?? 'text-ink-2'}`}>{value}</span>
    </>
  )
}

/**
 * 收纳态：触发器只写一个名词 `账本 ▾`，不留数字；展开是全部五项（意图、事实、
 * 证据、在跑、快照已载）。收起的数字放在触发器的 title 里，悬停即可读到。
 *
 * 面板是**内联绝对定位**，不开 Portal；展开有两条路：
 *   1) CSS：`group-hover` / `group-focus-within`（与同一行的 tokens 用量同一机制）；
 *   2) JS：`pointerdown` 且 `pointerType=touch` 时直接打开。
 * 第二条是必须的：Firefox 里手指点按可能根本不送 `click`，也可能送两次（连点被当成
 * 双击），只靠 click 取反就会出现「箭头转了、面板一闪就没」，只靠 focus-within 又会
 * 受窗口聚焦影响。所以按下就开、随后 700ms 内的 click 一律忽略，关只走 Esc 和点外部。
 * 收起时留一份 sr-only「快照已载 …」：验收脚本与读屏都按这四个字找这条状态。
 */
export function BoardSummary({
  board,
  running,
  loadedNodes,
  totalNodes,
}: {
  board?: BoardResponse
  running: number
  loadedNodes?: number
  totalNodes: number
}) {
  const panelID = useId()
  const wrapRef = useRef<HTMLSpanElement>(null)
  // `open`：本轮被显式打开（触摸按下 / 鼠标点击 / 键盘回车）。
  // `suppressed`：被 Esc 或点外部收起了，盖过 CSS 的 hover/focus-within。
  const [open, setOpen] = useState(false)
  const [suppressed, setSuppressed] = useState(false)
  // 触屏上一次 tap 可能先来 pointerdown、再补一到两次 click。既然按下就开了，
  // 紧随其后的 click 一律忽略，不再取反——「箭头转了、面板一闪就没」就是这么来的。
  const touchAt = useRef(0)
  const snapshot = loadedNodes === undefined ? '节点数未读全' : `${loadedNodes}/${totalNodes} 节点`

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      setSuppressed(true)
    }
    const onPointerDown = (event: PointerEvent) => {
      const wrap = wrapRef.current
      if (wrap && event.target instanceof Node && wrap.contains(event.target)) return
      setOpen(false)
      setSuppressed(true)
    }
    document.addEventListener('keydown', onKeyDown, true)
    document.addEventListener('pointerdown', onPointerDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
      document.removeEventListener('pointerdown', onPointerDown, true)
    }
  }, [])

  const panelClass = suppressed
    ? 'pointer-events-none opacity-0'
    : open
      ? 'pointer-events-auto opacity-100'
      : 'pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100'

  return (
    <span
      ref={wrapRef}
      className="group relative inline-flex items-center"
      onFocus={() => setSuppressed(false)}
      data-testid="board-counts"
      data-suppressed={suppressed ? 'true' : undefined}
    >
      <button
        type="button"
        className={TRIGGER}
        aria-controls={panelID}
        // 面板是一段说明文字，不是对话框：读屏按 group 读，数值直接作为按钮的描述，
        // 不必先「进入」一个并不存在的对话框（旧写法 aria-haspopup="dialog" 会这么承诺）。
        aria-describedby={panelID}
        onPointerDown={(event) => {
          // 触摸：按下就开，不等 click（Firefox 的 tap 不一定送 click，送两次也常见）。
          if (event.pointerType !== 'touch') return
          touchAt.current = Date.now()
          setSuppressed(false)
          setOpen(true)
        }}
        onClick={() => {
          if (Date.now() - touchAt.current < 700) return
          setSuppressed(false)
          setOpen((value) => !value)
        }}
        title={`账本计数：意图 ${fmtInt(board?.intents.total)} · 事实 ${fmtInt(board?.facts.total)}；点开看证据、在跑与快照节点`}
      >
        <span>账本</span>
        <ChevronDown
          aria-hidden
          className="h-3.5 w-3.5 text-ink-3 transition-transform motion-reduce:transition-none group-hover:rotate-180 group-focus-within:rotate-180"
        />
      </button>
      <div
        id={panelID}
        role="group"
        aria-label="账本计数"
        data-testid="board-counts-panel"
        className={`absolute left-0 top-full z-30 w-60 rounded-lg border border-hairline bg-popover p-2.5 text-[11px] shadow-elevated transition-opacity motion-reduce:transition-none lg:left-auto lg:right-0 ${panelClass}`}
      >
        <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
          <Row label="意图" value={fmtInt(board?.intents.total)} />
          <Row label="事实" value={fmtInt(board?.facts.total)} />
          <Row label="证据" value={fmtInt(board?.evidence)} />
          <Row label="在跑" value={String(running)} dot={running > 0} tone={running > 0 ? 'text-primary' : undefined} />
          <Row label="快照已载" value={snapshot} />
        </div>
      </div>
      <span className="sr-only">快照已载 {snapshot}</span>
    </span>
  )
}

/** 平铺一行（其他页）：标签 11px ink-3、数值等宽 tabular ink-2，标签与数值之间 6px。 */
export function BoardCounters({ board, running }: { board?: BoardResponse; running: number }) {
  return (
    <div className="flex items-center text-[11px] leading-4 text-ink-3">
      <span className="inline-flex items-baseline gap-1.5">
        意图
        <b className="font-mono text-[11px] font-semibold tabular-nums text-ink-2">{fmtInt(board?.intents.total)}</b>
      </span>
      <MetaRule />
      <span className="inline-flex items-baseline gap-1.5">
        事实
        <b className="font-mono text-[11px] font-semibold tabular-nums text-ink-2">{fmtInt(board?.facts.total)}</b>
      </span>
      <MetaRule />
      <span className="inline-flex items-baseline gap-1.5">
        证据
        <b className="font-mono text-[11px] font-semibold tabular-nums text-ink-2">{fmtInt(board?.evidence)}</b>
      </span>
      <MetaRule />
      <span className="inline-flex items-baseline gap-1.5">
        在跑
        <b
          className={`font-mono text-[11px] font-semibold tabular-nums ${running > 0 ? 'text-primary' : 'text-ink-2'}`}
        >
          {running}
        </b>
      </span>
    </div>
  )
}
