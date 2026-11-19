import { Bot, Terminal, UserRound } from 'lucide-react'
import { cn } from '@cyber/theme'
import type { ParticipantRole } from '@/lib/ioa'

/**
 * The identity mark on a chat row.
 *
 * It is a role icon, not the first character of the sender's name: one glyph of
 * 「操作员」 is 「操」, and a direction title has no meaningful first letter. The
 * glyph was also ambiguous — 指 / 操 / W told the operator nothing that the
 * sender line does not already say.
 *
 * Role colours follow the rest of the console: the operator gets the accent, the
 * commander the `ai` hue used by the commander card and its run badges, and a
 * direction session stays neutral.
 */
export function SenderAvatar({
  role,
  className,
}: {
  role: ParticipantRole
  className?: string
}) {
  const Icon = role === 'operator' ? UserRound : role === 'commander' ? Bot : Terminal
  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex h-6 w-6 shrink-0 items-center justify-center rounded-full border',
        role === 'operator'
          ? 'border-primary/30 bg-primary/12 text-primary'
          : role === 'commander'
            ? 'border-ai/25 bg-ai/12 text-ai'
            : 'border-hairline bg-surface-2 text-ink-2',
        className,
      )}
    >
      <Icon className="h-3.5 w-3.5" />
    </span>
  )
}
