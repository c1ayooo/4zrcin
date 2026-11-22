import * as React from 'react'
import { cn } from '@cyber/theme'

export interface ListRowProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  /** Selected state — soft-primary tint + `aria-pressed`. */
  active?: boolean
  /** Leading element (status dot, icon, avatar), top-aligned with the first line. */
  leading?: React.ReactNode
}

/**
 * A selectable, left-aligned multi-line list row: a leading glyph beside a
 * stacked body (typically two truncating lines). Owns the button chrome + the
 * soft-primary "selected" tint that the roster / agent / session lists
 * hand-rolled per call site. `Button` doesn't fit — it centres its content and
 * is single-line; this is a content row that reads as a control.
 */
export const ListRow = React.forwardRef<HTMLButtonElement, ListRowProps>(
  ({ active = false, leading, className, children, type = 'button', ...props }, ref) => (
    <button
      ref={ref}
      type={type}
      aria-pressed={active}
      className={cn(
        'flex w-full items-start gap-3 rounded-lg px-2.5 py-2.5 text-left transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
        active
          ? 'bg-primary/[0.09] text-ink shadow-[inset_2px_0_0_var(--primary)]'
          : 'text-ink-2 hover:bg-surface-2 hover:text-ink',
        className,
      )}
      {...props}
    >
      {leading}
      <span className="block min-w-0 flex-1">{children}</span>
    </button>
  ),
)
ListRow.displayName = 'ListRow'
