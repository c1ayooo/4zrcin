/** Scroll only the log viewport so the run selector and toolbar stay in place. */
export function scrollRunStreamToLatest(stream: HTMLElement | null): void {
	const viewport = runStreamViewport(stream)
  viewport?.scrollTo({ top: viewport.scrollHeight, behavior: 'smooth' })
}

export function runStreamViewport(stream:HTMLElement|null):HTMLElement|null {
	return stream?.closest<HTMLElement>('[data-radix-scroll-area-viewport]')??null
}
