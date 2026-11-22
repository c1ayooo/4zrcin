import * as React from 'react'
import { cn } from '@cyber/theme'

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {}

const Textarea = React.forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, ...props }, ref) => (
    <textarea
      className={cn(
        'flex min-h-[76px] w-full rounded-md border border-input bg-surface-2/50 px-2.5 py-2 text-[13px] leading-relaxed text-ink ring-offset-background',
        'transition-colors placeholder:text-ink-3 hover:border-ink-3/40',
        'focus-visible:border-ring/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35 focus-visible:ring-offset-0',
        'disabled:cursor-not-allowed disabled:border-border disabled:bg-muted disabled:opacity-50',
        className
      )}
      ref={ref}
      {...props}
    />
  )
)
Textarea.displayName = 'Textarea'

export { Textarea }
