import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { cn } from '@cyber/theme'

export type CalloutTone = 'info' | 'success' | 'warning' | 'destructive' | 'primary' | 'ai'

/* Tint carries the verdict; the text stays ink so it remains readable. */
const toneFrame: Record<CalloutTone, string> = {
  info: 'border-info/25 bg-info/[0.07]',
  primary: 'border-primary/25 bg-primary/[0.07]',
  success: 'border-success/25 bg-success/[0.07]',
  warning: 'border-warning/25 bg-warning/[0.07]',
  destructive: 'border-destructive/30 bg-destructive/[0.08]',
  ai: 'border-ai/25 bg-ai/[0.06]',
}

const toneIcon: Record<CalloutTone, string> = {
  info: 'text-info',
  primary: 'text-primary',
  success: 'text-success',
  warning: 'text-warning',
  destructive: 'text-destructive',
  ai: 'text-ai',
}

export interface CalloutProps {
  tone?: CalloutTone
  /** Leading glyph, top-aligned with the first line of the message. */
  icon?: ReactNode
  /** When set, renders a dismiss (×) button that calls this. */
  onDismiss?: () => void
  className?: string
  children: ReactNode
}

/**
 * A tinted inline alert box: leading icon + message on a role-coloured surface.
 * Replaces the ~10 hand-rolled `border-{tone}/30 bg-{tone}/10 text-{tone}` boxes
 * across the chat, config and session panels. Use `Badge` / `StatusIndicator`
 * for one-line status pills — Callout is for multi-line notices.
 */
export function Callout({ tone = 'info', icon, onDismiss, className, children }: CalloutProps) {
  return (
    <div
      role="status"
      className={cn(
        'flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-[12.5px] leading-relaxed text-ink-2',
        toneFrame[tone],
        className,
      )}
    >
      {icon && <span className={cn('mt-0.5 flex shrink-0 items-center', toneIcon[tone])}>{icon}</span>}
      <div className="min-w-0 flex-1">{children}</div>
      {onDismiss && (
        <button
          type="button"
          onClick={onDismiss}
          aria-label="Dismiss"
          className="-mr-1 shrink-0 rounded p-0.5 text-current opacity-50 transition hover:bg-foreground/10 hover:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  )
}
