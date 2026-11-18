// Selection focus eases the cold dim in instead of snapping it (render.ts
// applies the mix through focusColdAlpha). Hover is feedback, so it applies the
// dim immediately; a selection fades it.
//
// The state lives in the canvas between frames because the ease has to start
// from the dim that is actually on screen. Treating "no selection" as "no dim"
// made a click on a hovered node flash the whole map back to full brightness
// for a frame and then fade down again — hover already dimmed the map, so the
// selection starts from 1 there. A selection made with nothing hovered starts
// from 0 and eases in.

/** How long a selection focus takes to ease its dim in. */
export const FOCUS_FADE_MS = 220

export interface FocusFadeState {
  /** The focus set the running fade belongs to; null when nothing is selected. */
  set: Set<string> | null
  /** The dim the running fade starts from (0 = full brightness, 1 = cold). */
  from: number
  /** performance.now() when the running fade started. */
  start: number
  /** The dim painted by the last frame (0..1 of the cold alpha). */
  mix: number
}

export function initialFocusFade(): FocusFadeState {
  return { set: null, from: 0, start: 0, mix: 0 }
}

/**
 * The eased selection-focus mix for this frame. `target` is the cached focus
 * set (its identity is stable until the subject changes). `eases` is false for
 * hover frames and for "nothing selected": those paint the dim at full
 * strength without a transition, and remember that on-screen dim so a following
 * selection does not undo it.
 *
 * Returns the mix to paint and, while the fade runs, the time at which it ends
 * (the canvas' frame loop must keep painting until then).
 */
export function focusFadeMix(
  fade: FocusFadeState,
  target: Set<string> | null,
  eases: boolean,
  now: number,
): { mix: number; settledAt: number } {
  if (!eases || !target) {
    fade.set = null
    // A hovered subject is already dimmed on screen; no hover means undimmed.
    fade.mix = target ? 1 : 0
    return { mix: 1, settledAt: 0 }
  }
  if (fade.set !== target) {
    fade.from = fade.mix
    fade.set = target
    fade.start = now
    if (fade.from >= 1) {
      // The map is already fully dimmed (a hovered subject): there is nothing to
      // ease in, so do not keep the frame loop alive for an invisible fade.
      fade.mix = 1
      return { mix: 1, settledAt: 0 }
    }
  }
  const p = Math.min(1, (now - fade.start) / FOCUS_FADE_MS)
  const eased = 1 - Math.pow(1 - p, 3)
  fade.mix = fade.from + (1 - fade.from) * eased
  return { mix: fade.mix, settledAt: p < 1 ? fade.start + FOCUS_FADE_MS : 0 }
}
