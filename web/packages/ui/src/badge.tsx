import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@cyber/theme'

const badgeVariants = cva(
  'inline-flex items-center gap-1 border leading-none transition-colors focus:outline-none',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary-solid text-primary-foreground',
        secondary: 'border-hairline bg-secondary text-secondary-foreground',
        destructive: 'border-transparent bg-destructive text-destructive-foreground',
        outline: 'border-border bg-surface-2/60 text-ink-2',
        success: 'border-success/25 bg-success/12 text-success',
        warning: 'border-warning/25 bg-warning/12 text-warning',
        caution: 'border-caution/25 bg-caution/12 text-caution',
        info: 'border-info/25 bg-info/12 text-info',
        // Soft tonal fills completing the muted / primary / danger trio next to the
        // semantic ones above. Severity colours stay in lib/tones.ts — do NOT add
        // critical/high/low variants here (they'd duplicate destructive/caution/…).
        primary: 'border-primary/25 bg-primary/12 text-primary',
        muted: 'border-transparent bg-muted text-ink-3',
        danger: 'border-destructive/25 bg-destructive/12 text-destructive',
      },
      size: {
        // Pill — the original badge shape.
        default: 'rounded-full px-2 py-0.5 text-[11px] font-semibold',
        // Compact square chip for dense contexts (finding tags, asset facts), and
        // the leading-icon `gap`. Replaces the hand-rolled
        // `rounded px-1.5 py-0.5 text-[10px] font-medium` pills across the app.
        sm: 'rounded px-1.5 py-0.5 text-[11px] font-medium',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  }
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, size, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant, size }), className)} {...props} />
}

export { Badge, badgeVariants }
