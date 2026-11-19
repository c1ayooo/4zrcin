import { useEffect, useRef, useState } from 'react'
import { cn } from '@cyber/theme'
import { freshnessOf, type FreshnessState, type FreshnessTone } from '@/lib/freshness'

const TONE_DOT: Record<FreshnessTone, string> = {
  idle: 'bg-ink-3/50',
  live: 'bg-success',
  stale: 'bg-warning',
  lost: 'bg-destructive',
}

/**
 * One line that says how old the data on screen is. Polling is silent by
 * nature: without this the operator cannot tell "nothing happened" from "the
 * console stopped refreshing", and a broken poll would look like calm.
 */
export function FreshnessPill({
  state,
  intervalMs,
  label,
}: {
  state: FreshnessState
  /** The poll period this line describes; stale is two missed periods. */
  intervalMs: number
  /** What is being polled ("项目数据"), prefixed to the state. */
  label?: string
}) {
  // A one-second clock only moves the "N seconds ago" text; a fresh label keeps
  // the same string, so React does not touch the DOM on every tick.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  const freshness = freshnessOf(state, intervalMs, now)
  const live = freshness.tone === 'live'
  // Announce only when the state changes: the visible label re-renders every
  // second while stale, and a live region that repeated it would read the same
  // sentence once per tick.
  const [announcement, setAnnouncement] = useState('')
  const lastTone = useRef<FreshnessTone | null>(null)
  useEffect(() => {
    if (lastTone.current === freshness.tone) return
    lastTone.current = freshness.tone
    setAnnouncement(`${label ? `${label}：` : ''}${freshness.label}`)
  }, [freshness.tone, freshness.label, label])
  return (
    <span
      role="group"
      title={freshness.detail}
      className="flex min-w-0 items-center gap-1.5 px-1 py-0.5 text-[11px] leading-relaxed text-ink-3"
      data-testid="freshness-pill"
      data-tone={freshness.tone}
    >
      <span
        aria-hidden="true"
        className={cn('h-1.5 w-1.5 shrink-0 rounded-full', TONE_DOT[freshness.tone], live && 'motion-safe:animate-pulse')}
      />
      <span className="min-w-0 truncate">
        {label ? `${label} · ` : ''}
        {freshness.label}
      </span>
      <span className="sr-only" role="status" aria-live="polite">{announcement}</span>
    </span>
  )
}
