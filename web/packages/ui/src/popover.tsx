import * as React from 'react'
import * as PopoverPrimitive from '@radix-ui/react-popover'
import { cn } from '@cyber/theme'

/**
 * 触屏上的一次 tap 与桌面点击不是一回事（2026-11-24，与 `f7de1890` 的账本
 * 计数面板同一套处理）：Firefox 里手指点按可能根本不送 `click`，也可能送两次
 * （连点被当成双击）。Radix 的 Trigger 只在 `click` 上开合：
 * `node_modules/@radix-ui/react-popover/dist/index.mjs:110` 是
 * `onClick: composeEventHandlers(props.onClick, context.onOpenToggle)`。
 * 于是触摸下会「点一次没反应」，或者「开了又立刻关」。
 *
 * 这里在库里统一兜住，四个调用点（图谱「视图」、项目「更多」、通信「更多」、
 * 「状态说明」）不必各写一遍：
 *   - `pointerType === 'touch'` 的按下即开合（一次 tap 只来一次 pointerdown）；
 *   - 随后 700ms 内补来的 click 一并忽略，否则会被取反回去。
 * `onClick` 里 `preventDefault()` 能挡住 Radix 自己的开合：它用
 * `composeEventHandlers` 组合，内部处理器看 `event.defaultPrevented` 决定要不要跑。
 */
export const TOUCH_CLICK_GUARD_MS = 700

interface PopoverState {
  open: boolean
  onToggle: (next: boolean) => void
}

const PopoverStateContext = React.createContext<PopoverState | null>(null)

/**
 * 受控根：外壳自己持有开合，Trigger 才能在 pointerdown 里开合而不绕过 Radix。
 * 调用点没传 `open` 时退化成内部状态，行为与之前一致。
 */
function Popover({
  open,
  defaultOpen,
  onOpenChange,
  ...props
}: React.ComponentPropsWithoutRef<typeof PopoverPrimitive.Root>) {
  const [uncontrolled, setUncontrolled] = React.useState(Boolean(defaultOpen))
  const value = open ?? uncontrolled
  const set = React.useCallback(
    (next: boolean) => {
      if (open === undefined) setUncontrolled(next)
      onOpenChange?.(next)
    },
    [open, onOpenChange],
  )
  const state = React.useMemo(() => ({ open: value, onToggle: set }), [value, set])
  return (
    <PopoverStateContext.Provider value={state}>
      <PopoverPrimitive.Root {...props} open={value} onOpenChange={set} />
    </PopoverStateContext.Provider>
  )
}

const PopoverTrigger = React.forwardRef<
  React.ElementRef<typeof PopoverPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof PopoverPrimitive.Trigger>
>(({ onPointerDown, onClick, ...props }, ref) => {
  const state = React.useContext(PopoverStateContext)
  // 最近一次触摸按下的时刻；0 表示这一轮还没按过（鼠标点击走原路）。
  const touchAt = React.useRef(0)

  return (
    <PopoverPrimitive.Trigger
      ref={ref}
      {...props}
      onPointerDown={(event) => {
        onPointerDown?.(event)
        if (event.pointerType !== 'touch' || !state) return
        touchAt.current = Date.now()
        state.onToggle(!state.open)
      }}
      onClick={(event) => {
        // 按下已经开合过：同一次 tap 补来的 click（一到两次）不能再取反。
        if (Date.now() - touchAt.current < TOUCH_CLICK_GUARD_MS) {
          event.preventDefault()
          return
        }
        onClick?.(event)
      }}
    />
  )
})
PopoverTrigger.displayName = 'PopoverTrigger'

const PopoverClose = PopoverPrimitive.Close

const PopoverContent = React.forwardRef<
  React.ElementRef<typeof PopoverPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof PopoverPrimitive.Content>
>(({ className, align = 'center', sideOffset = 4, ...props }, ref) => (
  <PopoverPrimitive.Portal>
    <PopoverPrimitive.Content
      ref={ref}
      align={align}
      sideOffset={sideOffset}
      className={cn(
        'z-50 w-72 rounded-lg border border-hairline bg-popover p-3 text-popover-foreground shadow-elevated outline-none data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
        className
      )}
      {...props}
    />
  </PopoverPrimitive.Portal>
))
PopoverContent.displayName = PopoverPrimitive.Content.displayName

export { Popover, PopoverTrigger, PopoverClose, PopoverContent }
