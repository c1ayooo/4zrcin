import type { ReactNode } from 'react'
import { cn } from '@cyber/theme'
import { Badge } from './badge'

export type StatusKind = 'idle' | 'running' | 'success' | 'error' | 'pending' | 'warning' | 'info' | 'online'

export interface StatusIndicatorProps {
  status: StatusKind
  label?: string
  /** Leading glyph. Defaults to a coloured dot; pass an icon to show it instead. */
  icon?: ReactNode
  showDot?: boolean
  /** Square, un-capitalised chip (not a full pill) for dense/inline contexts. */
  compact?: boolean
  className?: string
}

const dotColors: Record<StatusKind, string> = {
  idle: 'bg-ink-3',
  running: 'bg-info animate-pulse',
  success: 'bg-success',
  error: 'bg-destructive',
  pending: 'bg-warning animate-pulse',
  warning: 'bg-warning',
  info: 'bg-info',
  online: 'bg-primary',
}

const badgeStyles: Record<StatusKind, string> = {
  idle: 'bg-surface-2 text-ink-3 border-border',
  running: 'bg-info/12 text-info border-info/20',
  success: 'bg-success/12 text-success border-success/20',
  error: 'bg-destructive/12 text-destructive border-destructive/25',
  pending: 'bg-warning/12 text-warning border-warning/20',
  warning: 'bg-warning/12 text-warning border-warning/20',
  info: 'bg-info/12 text-info border-info/20',
  online: 'bg-primary/12 text-primary border-primary/25',
}

export function StatusIndicator({ status, label, icon, showDot = true, compact = false, className }: StatusIndicatorProps) {
  const displayLabel = label || status

  return (
    <Badge
      variant="outline"
      className={cn(
        'gap-1.5 px-2 py-0 text-[11px] font-medium leading-4',
        compact ? 'rounded' : 'rounded-full capitalize',
        badgeStyles[status],
        className,
      )}
    >
      {icon ? (
        <span className="flex shrink-0 items-center">{icon}</span>
      ) : (
        showDot && <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', dotColors[status])} />
      )}
      {displayLabel}
    </Badge>
  )
}

export function StatusDot({ status, className }: { status: StatusKind; className?: string }) {
  return (
    <span
      className={cn('inline-block h-2 w-2 shrink-0 rounded-full', dotColors[status], className)}
      aria-label={status}
    />
  )
}
