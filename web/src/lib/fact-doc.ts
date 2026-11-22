// The fact's full text (断言 + 复现过程) lives on the detail read, not on the
// graph's lightweight fact record: the graph payload carries `doc` only sometimes
// (older snapshots / partial rows), so the panel used to show the summary and
// never the body. These two pure bits decide *which* doc to show and whether it
// needs the 展开全文 toggle, so the panel does not re-derive that rule inline.

/**
 * Above this many characters the body offers the 收起 / 展开全文 toggle. The
 * body itself still renders expanded by default; the toggle is a way to shrink a
 * wall of text back to a preview, not a gate in front of it.
 */
export const FACT_DOC_EXPAND_CHARS = 600

/** Clamp to a string; a missing/legacy field is empty, never "undefined". */
function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/**
 * The doc to render: the detail read's fact wins, the graph record is the
 * fallback. A blank (or whitespace-only) primary does not shadow a real
 * fallback, and an all-blank pair is empty — the caller renders nothing rather
 * than an empty box.
 */
export function factDocText(primary?: string | null, fallback?: string | null): string {
  const first = text(primary)
  if (first.trim() !== '') return first
  const second = text(fallback)
  return second.trim() !== '' ? second : ''
}

/** True when the body is long enough to offer the 收起 / 展开全文 toggle. */
export function factDocLong(doc: string): boolean {
  return doc.length > FACT_DOC_EXPAND_CHARS
}
