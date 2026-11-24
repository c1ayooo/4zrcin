// 图谱页里「关掉浮层之后键盘该落在哪」只有一处实现：主画布。
//
// 画布是页面上唯一的常驻焦点位置（`GraphListView` 的列表关闭、节点详情的
// X / Esc 都回到它）。焦点掉到 `BODY` 时，下一次 Tab 会从文档头
// 重新开始，键盘用户等于被送回起点——2026-11-24 巡查实测过这一条。
//
// 关闭动作会卸载触发它的按钮，所以这里等一帧再找画布：父组件可能刚好在重挂载，
// 找不到就静默跳过（没有画布的测试渲染里不该因此报错）。
export const GRAPH_CANVAS_TESTID = 'graph-canvas'

/** 焦点回主画布；`preventScroll` 免得把操作员刚滚到的位置跳回去。 */
export function focusGraphCanvas(): void {
  if (typeof window === 'undefined') return
  window.requestAnimationFrame(() => {
    document
      .querySelector<HTMLElement>(`[data-testid="${GRAPH_CANVAS_TESTID}"]`)
      ?.focus({ preventScroll: true })
  })
}

/**
 * 正在输入时按 Esc 不该关掉整个浮层（会连没提交的文本一起丢）。
 * 只挡真正接收文字的元素；按钮、链接上的 Esc 照常关闭。
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  // 测试渲染与 SSR 没有 HTMLElement；那里没有「正在输入」这回事。
  if (typeof HTMLElement === 'undefined' || !(target instanceof HTMLElement)) return false
  const tag = target.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return target.isContentEditable
}

/**
 * Esc 关浮层的统一判据。
 *
 * `defaultPrevented` 那一条不是防御性代码，是实测出来的：证据预览、确认框这类
 * Portal 弹层，在 React 树里仍是面板的子节点，keydown 会按 React 树冒到面板上；
 * 而 Radix 的 `useEscapeKeydown` 在 document 的**捕获阶段**先跑，处理完自己会
 * `preventDefault()`。不认这个标记，一次 Esc 会既关弹层又关整块节点面板
 * （2026-11-24 复现：预览开着按 Esc，弹层关、面板跟着消失）。
 */
export function isEscapeToClose(event: {
  key: string
  defaultPrevented: boolean
  target: EventTarget | null
}): boolean {
  return event.key === 'Escape' && !event.defaultPrevented && !isTypingTarget(event.target)
}

/** 关掉浮层并把焦点交回主画布。 */
export function closeAndFocusCanvas(close: () => void): void {
  close()
  focusGraphCanvas()
}
