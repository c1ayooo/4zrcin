const navigationEvent = 'tw:navigate'

export function locationPath(): string {
  return window.location.pathname + window.location.search + window.location.hash
}

/** Only the parts of the View Transitions API this module uses. */
type ViewTransitionLike = {
  ready?: Promise<void>
  finished: Promise<void>
  updateCallbackDone?: Promise<void>
}
type ViewTransitionDocument = Document & {
  startViewTransition?: (update: () => void | Promise<void>) => ViewTransitionLike
}

/** The transition an animation currently owns; a second navigation skips it. */
let activeTransition: ViewTransitionLike | null = null

function prefersReducedMotion(): boolean {
  try {
    return typeof window !== 'undefined'
      && typeof window.matchMedia === 'function'
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    // A restricted embed can throw from matchMedia; motion is optional, so treat
    // it as "no preference" rather than failing the navigation.
    return false
  }
}

/**
 * One frame: the browser has committed React's render and laid it out, so the
 * incoming view is what the new snapshot captures. Two frames used to be here;
 * a single frame is enough and keeps the update callback far inside the
 * browser's DOM-update budget on a busy page (a graph render can rival it).
 */
function settled(): Promise<void> {
  if (typeof requestAnimationFrame !== 'function') return Promise.resolve()
  return new Promise((resolve) => requestAnimationFrame(() => resolve()))
}

/**
 * Same-document navigation must notify React as well as the browser history.
 *
 * The route change is a progressive enhancement: where the View Transitions API
 * exists and the operator has not asked for reduced motion, the history change
 * is wrapped in `document.startViewTransition` so the outgoing and incoming
 * views cross-fade. The URL/history mutation never depends on the transition:
 *
 *   - every transition promise is observed (`.catch`), because an aborted or
 *     skipped transition otherwise surfaces as an unhandled page error and the
 *     console's acceptance scripts treat any runtime error as a failure;
 *   - a short fallback timer commits the navigation if the browser skips the
 *     update callback (hidden tab, new transition, DOM-update timeout), so a
 *     navigation is never lost;
 *   - while one transition animates, the next navigation commits immediately
 *     without starting a second one (a second start would skip the first and
 *     can drop the new URL's commit to a callback that never runs).
 */
export function navigate(url: string, replace = false): void {
  if (url === locationPath()) return
  let committed = false
  let fallback: ReturnType<typeof setTimeout> | undefined
  const commit = () => {
    if (committed) return
    committed = true
    if (fallback !== undefined) clearTimeout(fallback)
    if (replace) window.history.replaceState(null, '', url)
    else window.history.pushState(null, '', url)
    window.dispatchEvent(new Event(navigationEvent))
  }
  const doc = typeof document === 'undefined' ? undefined : (document as ViewTransitionDocument)
  if (typeof doc?.startViewTransition !== 'function' || prefersReducedMotion() || activeTransition) {
    commit()
    return
  }
  let transition: ViewTransitionLike
  try {
    transition = doc.startViewTransition(async () => {
      commit()
      await settled()
    })
  } catch {
    // The API is new: a browser that exposes it but refuses this call must still
    // get the navigation, and it must only happen once.
    commit()
    return
  }
  activeTransition = transition
  // Never leave a rejected transition promise unobserved: an aborted animation
  // is cosmetic, not a failed navigation.
  transition.ready?.catch(() => {})
  const release = () => { if (activeTransition === transition) activeTransition = null }
  transition.finished.catch(() => {}).then(release)
  // If the update callback never runs (skip/timeout), the navigation still has
  // to happen: the browser's DOM-update budget is a few seconds, so 750ms is a
  // safe fallback that keeps a deep link clickable under load.
  fallback = setTimeout(commit, 750)
}

export function subscribeLocation(changed: () => void): () => void {
  const events = ['popstate', 'hashchange', navigationEvent]
  for (const event of events) window.addEventListener(event, changed)
  return () => { for (const event of events) window.removeEventListener(event, changed) }
}

/** A damaged or hand-written escape must not prevent the workspace rendering. */
export function decodeLocationPart(value: string): string {
  try { return decodeURIComponent(value) }
  catch { return value }
}
