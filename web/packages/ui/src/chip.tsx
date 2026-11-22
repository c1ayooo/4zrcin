import * as React from 'react'
import { cn } from '@cyber/theme'

export interface ChipProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean
}

/**
 * A toggleable filter chip — a small pill button that reads as selected (soft
 * primary) or unselected (ink-2, hover to ink). The recurring segmented-filter
 * control in the findings/asset views (severity filters, view tabs).
 * Shape/size is the caller's via className (rounded-full ↔ rounded); this owns
 * the active/inactive colour logic that was hand-rolled per call site.
 */
export const Chip = React.forwardRef<HTMLButtonElement, ChipProps>(
  ({ active = false, className, type = 'button', ...props }, ref) => (
    <button
      ref={ref}
      type={type}
      aria-pressed={active}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[12px] font-medium leading-4 transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-offset-1 focus-visible:ring-offset-background',
        active
          ? 'border-primary/35 bg-primary/12 text-primary'
          : 'border-border bg-transparent text-ink-2 hover:border-ink-3/40 hover:bg-surface-2 hover:text-ink',
        className,
      )}
      {...props}
    />
  )
)
Chip.displayName = 'Chip'
