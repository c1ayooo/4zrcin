import type { LucideIcon } from 'lucide-react'
import { cn } from '@cyber/theme'

/** One rail item: icon + label, with the active row reading as "you are here". */
export function SideNav({
  active,
  icon: Icon,
  label,
  onClick,
  onIntent,
  badge,
}: {
  active: boolean
  icon: LucideIcon
  label: string
  onClick: () => void
  /**
   * Fired when the row is reached for (hover, focus, pointer down), not only
   * when it activates. The shell uses it to start downloading the page's chunk,
   * so the click swaps content instead of showing the fallback. Must stay
   * side-effect-only: it never changes the row.
   */
  onIntent?: () => void
  badge?: number
}) {
  return (
    <button
      type="button"
      aria-current={active ? 'page' : undefined}
      // The label already carries the name; the badge is a live count appended to
      // it, and the active row announces "current page" through aria-current, so
      // no extra aria-label can drift out of sync with the visible text.
      onClick={onClick}
      // "Reached for" covers hover, keyboard focus and a finger: a touch device
      // never fires mouseenter, and pointerdown lands before the click, so the
      // chunk is already on its way when the row activates.
      onMouseEnter={onIntent}
      onFocus={onIntent}
      onPointerDown={onIntent}
      className={cn(
        'tw-nav-item group relative flex min-h-10 w-full items-center gap-2.5 rounded-lg px-3 py-1.5 text-left text-ui transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
        active ? 'font-semibold text-ink' : 'font-normal text-ink-2 hover:bg-surface-2 hover:text-ink',
      )}
    >
      <Icon aria-hidden="true" className={cn('h-4 w-4 shrink-0', active ? 'text-primary' : 'text-ink-3 group-hover:text-ink-2')} />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {badge !== undefined && badge > 0 && (
        <span className="tw-nums rounded-full bg-warning/15 px-1.5 py-0.5 font-mono text-[11px] leading-none text-warning">
          {badge}
        </span>
      )}
    </button>
  )
}
