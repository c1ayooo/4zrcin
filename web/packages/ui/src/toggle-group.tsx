import * as React from 'react'
import { cn } from '@cyber/theme'

interface ToggleGroupProps {
  value: string
  onValueChange: (value: string) => void
  children: React.ReactNode
  className?: string
  disabled?: boolean
  ariaLabel?: string
}

function ToggleGroup({ value, onValueChange, children, className, disabled, ariaLabel }: ToggleGroupProps) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      className={cn('inline-flex items-center rounded-lg border border-hairline bg-surface-2/60 p-0.5', className)}
    >
      {React.Children.map(children, (child) => {
        if (React.isValidElement<ToggleGroupItemProps>(child)) {
          return React.cloneElement(child, {
            active: child.props.value === value,
            onClick: () => !disabled && onValueChange(child.props.value),
            disabled,
          })
        }
        return child
      })}
    </div>
  )
}

interface ToggleGroupItemProps {
  value: string
  children: React.ReactNode
  className?: string
  active?: boolean
  onClick?: () => void
  disabled?: boolean
}

function ToggleGroupItem({ children, className, active, onClick, disabled }: ToggleGroupItemProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex items-center justify-center rounded-md px-2.5 py-1 text-[12px] font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
        // Selection is a raised surface, not a second brand accent: the red CTA
        // must stay the only saturated control on a page.
        active
          ? 'bg-surface-3 text-ink shadow-soft'
          : 'text-ink-2 hover:bg-surface-3 hover:text-ink',
        disabled && 'cursor-not-allowed opacity-40',
        className
      )}
    >
      {children}
    </button>
  )
}

export { ToggleGroup, ToggleGroupItem }
