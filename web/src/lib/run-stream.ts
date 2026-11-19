/** Scroll only the log viewport so the run selector and toolbar stay in place.
 *  The caller chooses the motion: the toolbar's explicit jump is instant, while
 *  the default stays smooth for any other use. */
export function scrollRunStreamToLatest(
  stream: HTMLElement | null,
  behavior: ScrollBehavior = 'smooth',
): void {
  const viewport = runStreamViewport(stream)
  viewport?.scrollTo({ top: viewport.scrollHeight, behavior })
}

export function runStreamViewport(stream:HTMLElement|null):HTMLElement|null {
	return stream?.closest<HTMLElement>('[data-radix-scroll-area-viewport]')??null
}
