import * as React from 'react'
import { cn } from '@cyber/theme'

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      className={cn(
        'flex h-9 w-full rounded-md border border-input bg-surface-2/50 px-2.5 py-1.5 text-ui text-ink ring-offset-background',
        'transition-colors placeholder:text-ink-3 hover:border-ink-3/40',
        'focus-visible:border-ring/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35 focus-visible:ring-offset-0',
        'file:border-0 file:bg-transparent file:text-ui file:font-medium',
        'disabled:cursor-not-allowed disabled:border-border disabled:bg-muted disabled:opacity-50',
        className
      )}
      ref={ref}
      {...props}
    />
  )
)
Input.displayName = 'Input'

export { Input }
