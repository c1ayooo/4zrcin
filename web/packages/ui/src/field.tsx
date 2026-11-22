import type { ReactNode } from 'react'
import { cn } from '@cyber/theme'

export interface FieldProps {
  label: ReactNode
  /** Muted helper text under the control. */
  hint?: ReactNode
  /** Error text; when set it replaces the hint and reads destructive. */
  error?: ReactNode
  className?: string
  children: ReactNode
}

/**
 * A labelled form row: label above a control, with an optional hint / error line
 * below. The `<label>` wrapper implicitly associates with the single control it
 * wraps. Labels sit at ink-2 (not the faintest grey) so forms read crisp —
 * matching the CJK type notes (MiSans has two weights, so label legibility comes
 * from ink colour, not a heavier face). Promoted out of ConfigPanel, where it was
 * hand-defined and used ~23×.
 */
export function Field({ label, hint, error, className, children }: FieldProps) {
  return (
    <label className={cn('block space-y-1.5', className)}>
      <span className="block text-[12px] font-medium leading-4 text-ink-2">{label}</span>
      {children}
      {error ? (
        <span className="block text-[11px] leading-4 text-destructive">{error}</span>
      ) : hint ? (
        <span className="block text-[11px] leading-4 text-ink-3">{hint}</span>
      ) : null}
    </label>
  )
}
