import type { ReactNode } from 'react'
import { Inbox, type LucideIcon } from 'lucide-react'
import { cn } from '@cyber/theme'

export interface EmptyStateProps {
  icon?: LucideIcon
  title: string
  description?: string
  action?: ReactNode
  compact?: boolean
  className?: string
  children?: ReactNode
}

/**
 * The console's empty state: a recessed glyph block, a one-line title and a
 * short explanation that says what will appear here. It is deliberately louder
 * than a caption (so an empty panel never reads as "broken") but quieter than
 * a normal card body (so it never competes with real data).
 */
export function EmptyState({
  icon: Icon = Inbox,
  title,
  description,
  action,
  compact = false,
  className,
  children,
}: EmptyStateProps) {
  return (
    <div className={cn(
      'flex flex-col items-center justify-center gap-3.5 text-center',
      compact ? 'gap-2.5 px-4 py-8' : 'px-6 py-14',
      className
    )}>
      <div className={cn(
        'flex items-center justify-center rounded-xl border border-hairline bg-surface-2',
        compact ? 'h-9 w-9' : 'h-12 w-12'
      )}>
        <Icon className={cn(
          'text-ink-3',
          compact ? 'h-4 w-4' : 'h-5 w-5'
        )} />
      </div>
      <div className="space-y-1">
        <h3 className={cn(
          'font-semibold tracking-tight text-ink',
          compact ? 'text-[13px]' : 'text-[14px]'
        )}>
          {title}
        </h3>
        {description && (
          <p className={cn(
            'max-w-[340px] leading-relaxed text-ink-2',
            compact ? 'text-[12px]' : 'text-[12.5px]'
          )}>
            {description}
          </p>
        )}
      </div>
      {action && <div className="mt-1">{action}</div>}
      {children}
    </div>
  )
}
