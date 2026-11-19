import { useCallback, useLayoutEffect, useRef, useState, type RefObject } from 'react'

/**
 * How close (px) the viewport must be to the end for "following the latest" to
 * stay on. A chat that is 20px from the bottom is reading the newest line, not
 * reviewing history; the threshold also absorbs sub-pixel layout rounding.
 */
export const LATEST_THRESHOLD_PX = 56

/** True when the viewport is at (or within `threshold` of) its bottom edge. */
export function viewportAtLatest(viewport: HTMLElement | null, threshold = LATEST_THRESHOLD_PX): boolean {
  if (!viewport) return true
  return viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= threshold
}

/**
 * Scroll only the viewport itself. `scrollIntoView` would also move ancestor
 * scroll containers and the page, which is wrong for a nested chat pane.
 */
export function scrollViewportToLatest(
  viewport: HTMLElement | null,
  behavior: ScrollBehavior = 'auto',
): void {
  if (!viewport) return
  if (typeof viewport.scrollTo === 'function') {
    viewport.scrollTo({ top: viewport.scrollHeight, behavior })
    return
  }
  viewport.scrollTop = viewport.scrollHeight
}

/** Module-level identity keeps the hook's default resolver referentially stable;
 *  an inline default would re-run the reset effect on every render. */
function sameViewport<T extends HTMLElement>(node: T): T {
  return node
}

export interface LatestScroll<T extends HTMLElement> {
  /** Attach to the content node inside the scroll viewport. */
  ref: RefObject<T>
  /** Whether the view is following new rows at the bottom. */
  pinned: boolean
  /** Rows appended while the operator was reading history (since `pinned` last held). */
  unread: number
  /** Scroll to the newest row and resume following. */
  jump: () => void
}

/**
 * Keep a chronological pane at its newest row.
 *
 * - A new `resetKey` (another run / conversation) starts pinned to the bottom.
 * - While pinned, appending rows keeps the view at the bottom.
 * - Once the operator scrolls up, appends only raise `unread`; the explicit
 *   `jump()` is the one way back.
 *
 * The viewport is resolved from the content node so the same hook works for a
 * native `overflow-y:auto` div and for Radix's ScrollArea viewport.
 */
export function useLatestScroll<T extends HTMLElement>(
  itemCount: number,
  resetKey: string,
  resolveViewport: (node: T) => HTMLElement | null = sameViewport,
  /** Reuse a ref already owned by the caller (e.g. scroll-memory's root). */
  externalRef?: RefObject<T>,
): LatestScroll<T> {
  const ownRef = useRef<T>(null)
  const ref = externalRef ?? ownRef
  const [pinned, setPinned] = useState(true)
  const [unread, setUnread] = useState(0)
  const pinnedRef = useRef(true)
  const previousCount = useRef(itemCount)
  // A smooth jump emits intermediate scroll events that look like the operator
  // scrolling away; ignore them until the animation settles.
  const jumpUntil = useRef(0)

  const resolve = useCallback(
    () => (ref.current ? resolveViewport(ref.current) : null),
    [resolveViewport],
  )

  const jump = useCallback(() => {
    const viewport = resolve()
    if (!viewport) return
    jumpUntil.current = Date.now() + 600
    pinnedRef.current = true
    setPinned(true)
    setUnread(0)
    // A chat "jump" is a jump, not a long animated journey through 17k px of
    // history that leaves the button on screen the whole way.
    scrollViewportToLatest(viewport, 'auto')
  }, [resolve])

  // Track the real scroll position. `pinned` only flips when it changes, so
  // scrolling does not re-render every row on every wheel event.
  useLayoutEffect(() => {
    const viewport = resolve()
    if (!viewport) return
    const update = () => {
      if (Date.now() < jumpUntil.current) return
      const atLatest = viewportAtLatest(viewport)
      if (atLatest) {
        if (!pinnedRef.current) {
          pinnedRef.current = true
          setPinned(true)
        }
        setUnread(0)
        return
      }
      if (pinnedRef.current) {
        pinnedRef.current = false
        setPinned(false)
      }
    }
    update()
    viewport.addEventListener('scroll', update, { passive: true })
    return () => viewport.removeEventListener('scroll', update)
  }, [resetKey, resolve])

  // Switching the record starts at its newest row, even before the first poll
  // returns. The frame wait lets the browser lay the new rows out first.
  useLayoutEffect(() => {
    previousCount.current = itemCount
    pinnedRef.current = true
    setPinned(true)
    setUnread(0)
    const run = () => {
      const viewport = resolve()
      if (viewport) scrollViewportToLatest(viewport)
    }
    // A frame lets the browser lay the new rows out first; environments without
    // rAF (SSR/unit tests) still get the one-shot attempt.
    if (typeof requestAnimationFrame === 'function') {
      const frame = requestAnimationFrame(run)
      return () => cancelAnimationFrame(frame)
    }
    const timer = setTimeout(run, 0)
    return () => clearTimeout(timer)
  }, [resetKey, resolve])

  // Appends follow the bottom only while pinned; otherwise they count as unread.
  useLayoutEffect(() => {
    const delta = itemCount - previousCount.current
    previousCount.current = itemCount
    if (delta <= 0) return
    if (pinnedRef.current) {
      const viewport = resolve()
      if (viewport) scrollViewportToLatest(viewport)
      return
    }
    setUnread((current) => current + delta)
  }, [itemCount, resolve])

  return { ref, pinned, unread, jump }
}
