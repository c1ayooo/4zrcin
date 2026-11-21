import { createContext, useContext, useId, type ReactNode } from 'react'
import { cn } from '@cyber/theme'
import { Callout } from '@cyber/ui'
import { useScrollMemory } from '@/lib/view-memory'

/**
 * Project-level chrome injected into every page header while a project is open:
 * the live board counters and the controls that act on the whole engagement
 * (pause/resume, new direction, run a round). The project shell no longer draws
 * a second title bar of its own — one banner per page.
 *
 * Header hierarchy (top → bottom, strongest → weakest):
 *   1. the page title           20px / 600 / ink-1
 *   2. the actions              one primary CTA, the rest outline / ghost
 *   3. the subtitle             12.5px / ink-2, capped to a readable measure
 *   4. the eyebrow + meta chips 11px / ink-3, mono for counters
 * The eye lands on the title, then the primary action, and only then reads the
 * supporting counters — the inverse of a header where everything is a chip.
 */
export interface ProjectChrome {
  stats?: ReactNode
  controls?: ReactNode
  error?: string
  projectName?: string
}

export const ProjectChromeContext = createContext<ProjectChrome | null>(null)

export const pageGutter = 'px-3.5 sm:px-6 lg:px-8'

interface PageProps {
  title: string
  /** The project's live stance, in the one slot that never moves: immediately
   *  after the page name. It answers "is this thing alive" on every page, so it
   *  must not sit among the reference counters that change once a session. */
  status?: ReactNode
  subtitle?: ReactNode
  /** Reference only: snapshot time, layout mode, token totals. One quiet line
   *  beside the actions — never a second row under the title. */
  meta?: ReactNode
  actions?: ReactNode
  children: ReactNode
  /** Views that manage their own scrolling (split panes) pass false. */
  scroll?: boolean
  className?: string
}

/** The frame every view sits in: one header row, then a body that owns the scroll. */
export function Page({ title, status, subtitle, meta, actions, children, scroll = true, className }: PageProps) {
  const chrome = useContext(ProjectChromeContext)
  // The reading position survives leaving and re-entering a view (docs/34 §2).
  // A page that owns its own panes (`scroll={false}`) opts out by not attaching
  // the ref — the hook is still mounted, so hooks never become conditional.
  const scrollRef = useScrollMemory<HTMLDivElement>()
  const headingID = useId()
  const hasTopRow = Boolean(chrome?.controls || actions)
  const hasMetaRow = Boolean(chrome?.stats || meta)
  return (
    // The page body is the app's one <main> landmark, and the shell's skip link
    // targets it by id. `tabIndex={-1}` lets the skip link move focus here
    // without adding a tab stop of its own.
    <main
      id="tw-main"
      tabIndex={-1}
      aria-labelledby={headingID}
      className="flex h-full min-h-0 min-w-0 flex-col focus:outline-none"
    >
      {/* One row, ordered by how often the eye needs it: where this is (11px
          project name, 20px page name), the live stance (the one chip that
          never moves), then reference (snapshot / layout / tokens) and finally
          the actions. A subtitle still gets its own line, because it is prose,
          not a counter. */}
      <header className="tw-page-header flex shrink-0 flex-wrap items-center gap-x-6 gap-y-2 border-b border-hairline px-5 py-3 sm:px-6 lg:px-8">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
            <span className="tw-eyebrow min-w-0 max-w-[16rem] truncate text-ink-3">
              {chrome ? (chrome.projectName || '项目工作区') : '全局控制台'}
            </span>
            <span aria-hidden="true" className="text-ink-3/45">/</span>
            <h1 id={headingID} className="min-w-0 break-words text-[20px] font-semibold leading-7 tracking-tight text-ink">{title}</h1>
            {status}
          </div>
          {subtitle && (
            <div className="tw-subtitle mt-1 max-w-[760px]">{subtitle}</div>
          )}
        </div>
        {(hasTopRow || hasMetaRow) && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 xl:ml-auto">
            {hasMetaRow && (
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] leading-4">
                {chrome?.stats}
                {meta}
              </div>
            )}
            {hasTopRow && (
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                {chrome?.controls}
                {actions}
              </div>
            )}
          </div>
        )}

      </header>
      {chrome?.error && (
        <div className={cn('shrink-0 pt-4', pageGutter)} role="alert">
          <Callout tone="destructive">{chrome.error}</Callout>
        </div>
      )}
      <div
        ref={scroll ? scrollRef : null}
        // Stable handle for the acceptance scripts that check scroll memory
        // (docs/34 §2): only the page body that actually owns the scroll.
        data-testid={scroll ? 'page-scroll' : undefined}
        className={cn(
          'min-h-0 min-w-0 flex-1',
          scroll ? cn('overflow-y-auto py-4 sm:py-5', pageGutter) : 'overflow-hidden',
          className,
        )}
      >
        {children}
      </div>
    </main>
  )
}

/**
 * A titled block inside a page. Cards stay flat: one hairline border and a
 * card tint carry the structure — no nesting of "card inside card inside card",
 * and no drop shadow stacked under a tinted header.
 *
 * Extra `<section>` attributes (`data-testid`, `aria-*`, `id`) pass through, so
 * a page that needs a stable test hook for its card can use the real card
 * component instead of hand-rolling a `tw-panel` section.
 */
export function Panel({
  title,
  extra,
  children,
  className,
  bodyClassName,
  ...rest
}: {
  title?: ReactNode
  extra?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
} & Omit<React.HTMLAttributes<HTMLElement>, 'title' | 'children'>) {
  return (
    <section {...rest} className={cn('tw-panel min-w-0 rounded-xl', className)}>
      {(title || extra) && (
        <header className="tw-panel-header flex flex-wrap items-center gap-2 border-b border-hairline px-4 py-3">
          <div className="tw-section-title min-w-0">{title}</div>
          <div className="ml-auto flex items-center gap-2">{extra}</div>
        </header>
      )}
      <div className={cn('p-4', bodyClassName)}>{children}</div>
    </section>
  )
}
