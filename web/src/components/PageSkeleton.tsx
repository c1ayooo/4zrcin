import { Skeleton, SkeletonList } from '@cyber/ui'

/**
 * Suspense fallback shaped like <Page>: the same header strip and card body, so
 * the frame does not jump when the lazy chunk lands and only the content
 * appears. Shared by both shells (global pages and the project rail's panels) so
 * the two do not drift into two different loading looks.
 *
 * It is deliberately still: no entrance fade, no scan beam, no staggered lift.
 * The fallback is only on screen while a chunk downloads, which on this console
 * is a moment, and animating the placeholder made a switch feel slower than it
 * was. The only thing that moves is cyber-ui's own `Skeleton` pulse, the design
 * system's loading affordance, and that stops under `prefers-reduced-motion`
 * through the shared stylesheet backstop.
 */
export function PageSkeleton() {
  return (
    <div className="flex h-full min-h-0 flex-col" aria-busy="true" aria-label="页面加载中">
      {/* Header strip: the title row's three placeholder lines and the action
          block, at the sizes <Page> uses. */}
      <div className="tw-page-header flex shrink-0 items-start gap-3 border-b border-hairline px-5 py-4 sm:px-6 lg:px-8">
        <div className="min-w-0 space-y-2">
          <Skeleton className="h-2.5 w-24" />
          <Skeleton className="h-5 w-28" />
          <Skeleton className="h-3 w-40 sm:w-72" />
        </div>
        <div className="ml-auto shrink-0">
          <Skeleton className="h-8 w-24" />
        </div>
      </div>
      {/* Body: the three metric cards and then the list card, already in the
          final geometry. */}
      <div className="min-h-0 flex-1 overflow-hidden px-5 py-5 sm:px-6 lg:px-8">
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
          {[0, 1, 2].map((index) => (
            <div key={index}>
              <Skeleton className="h-20 rounded-xl" />
            </div>
          ))}
        </div>
        <div className="mt-4">
          <div className="rounded-xl border border-hairline bg-card p-4">
            <SkeletonList count={5} />
          </div>
        </div>
      </div>
    </div>
  )
}
