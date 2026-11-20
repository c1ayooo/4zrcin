import { createContext, useContext, useId, type ReactNode } from 'react'
import { cn } from '@cyber/theme'
import { Callout } from '@cyber/ui'
import { FolderKanban } from 'lucide-react'
import { useScrollMemory } from '@/lib/view-memory'

/**
 * Project-level chrome injected into every page header while a project is open:
 * the live board counters and the controls that act on the whole engagement
 * (pause/resume, new direction, run a round). The project shell no longer draws
 * a second title bar of its own — one banner per page.
 *
 * Place the page name and its workspace on the left, with actions and status
 * aligned on the right when the available width permits.
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
  subtitle?: ReactNode
  /** Status/meta chips: snapshot, live toggle, token totals. Rendered on the
   *  lower header row so the top row reads "what can I do". */
  meta?: ReactNode
  actions?: ReactNode
  children: ReactNode
  /** Views that manage their own scrolling (split panes) pass false. */
  scroll?: boolean
  className?: string
}

/** The frame every view sits in: title row, then a body that owns the scroll. */
export function Page({ title, subtitle, meta, actions, children, scroll = true, className }: PageProps) {
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
      <header className={cn(
        'tw-page-header flex shrink-0 flex-col gap-2.5 border-b border-hairline py-3 sm:gap-3.5 sm:py-5',
        pageGutter,
        chrome ? 'xl:flex-row xl:items-center xl:gap-x-8' : 'md:flex-row md:items-center md:gap-x-8',
      )}>
        <div className="min-w-0 flex-1">
          {chrome && (
            <div className="tw-page-context mb-1 flex min-w-0 items-center gap-1.5">
              <FolderKanban aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
              <span className="truncate">{chrome.projectName || '项目工作区'}</span>
            </div>
          )}
          <h1 id={headingID} className="tw-page-title break-words text-ink">{title}</h1>
          {subtitle && (
            <div className="tw-subtitle mt-1 max-w-[65ch]">{subtitle}</div>
          )}
        </div>
        {(hasTopRow || hasMetaRow) && <div className={cn(
          'flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2',
          chrome ? 'xl:max-w-[60%] xl:flex-col xl:items-end' : 'md:max-w-[60%] md:flex-col md:items-end',
        )}>
          {hasTopRow && (
            <div className={cn('flex min-w-0 flex-wrap items-center gap-2', chrome ? 'xl:justify-end' : 'md:justify-end')}>
              {chrome?.controls}
              {actions}
            </div>
          )}
          {hasMetaRow && (
            <div className={cn('flex min-w-0 flex-wrap items-center gap-2', chrome ? 'xl:justify-end' : 'md:justify-end')}>
              {chrome?.stats}
              {meta}
            </div>
          )}
        </div>}
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
