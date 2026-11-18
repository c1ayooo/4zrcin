// Pure page algebra for the graph snapshot reader.
//
// Kept free of React and of the `api` runtime module so `node --test` can load
// it directly (Node strips the type-only import). The hook in graph-data.ts
// owns effects and requests; everything that can be decided from page data
// alone lives here and is tested in tests/graph-data.test.ts.

import type { GraphFactMeta, GraphRead } from './api.ts'

/**
 * Rows one page asks for. The server clamps at 500 rows *and* bounds every page
 * by a byte budget, so a page can come back shorter than this; the reader must
 * therefore page until the snapshot reports `has_more: false`, never until the
 * row count adds up.
 */
export const GRAPH_PAGE_LIMIT = 500
/**
 * Safety cap for the full-snapshot read. The map shows the whole ledger, so the
 * reader keeps paging until the snapshot is exhausted; this only stops a
 * pathological (or buggy) server from looping forever.
 */
export const GRAPH_ALL_MAX_PAGES = 200
/** A deep link is located with at most two pages: it asks for one neighborhood. */
export const GRAPH_FOCUS_MAX_PAGES = 2

/** Merge rows by id; the later page wins (same snapshot, refreshed row). */
function mergeByID<T extends { id: string }>(a: T[], b: T[]): T[] {
  const out = new Map<string, T>()
  for (const row of a) out.set(row.id, row)
  for (const row of b) out.set(row.id, row)
  return [...out.values()]
}

function mergeOptionalByID<T extends { id: string }>(a?: T[], b?: T[]): T[] | undefined {
  if (!a && !b) return undefined
  return mergeByID(a ?? [], b ?? [])
}

/** Edges are identified by the full relation, so a pin and a reference between
 *  the same two nodes both survive. */
const edgeKey = (e: { source: string; target: string; kind: string }) =>
  `${e.source}/${e.target}/${e.kind}`

function mergeEdges(
  a: { source: string; target: string; kind: string }[],
  b: { source: string; target: string; kind: string }[],
): { source: string; target: string; kind: string }[] {
  const out = new Map<string, { source: string; target: string; kind: string }>()
  for (const e of a) out.set(edgeKey(e), e)
  for (const e of b) out.set(edgeKey(e), e)
  return [...out.values()]
}

/**
 * Append `b` (a later page of the same fixed snapshot) to `a`.
 *
 * The four streams the graph is built from all merge by identity; the envelope
 * (cursor, totals, snapshot version) comes from the later page because that is
 * the one the reader must continue from. Totals are kept when a page omits
 * them so an older server still answers a usable envelope.
 */
export function mergeGraphPages(a: GraphRead, b: GraphRead): GraphRead {
  return {
    ...b,
    facts: mergeByID(a.facts, b.facts),
    intents: mergeByID(a.intents, b.intents),
    goals: mergeByID(a.goals, b.goals),
    edges: mergeEdges(a.edges, b.edges),
    boundary: mergeByID(a.boundary, b.boundary),
    details: mergeOptionalByID(a.details, b.details),
    fact_meta: mergeOptionalByID<GraphFactMeta>(a.fact_meta, b.fact_meta),
    intents_total: b.intents_total ?? a.intents_total,
    facts_total: b.facts_total ?? a.facts_total,
    goals_total: b.goals_total ?? a.goals_total,
    edges_total: b.edges_total ?? a.edges_total,
    boundary_total: b.boundary_total ?? a.boundary_total,
  }
}

/** Snapshot identity of a page: pages of one snapshot share this key. */
export function graphPageKey(page: Pick<GraphRead, 'snapshot_version'>): string {
  return `${page.snapshot_version.epoch}:${page.snapshot_version.seq}`
}

/** Nodes a graph page contributes: facts + intents + goals (steps are derived). */
export function graphNodeCount(
  page: Pick<GraphRead, 'facts' | 'intents' | 'goals'>,
): number {
  return page.facts.length + page.intents.length + page.goals.length
}

/**
 * The snapshot root a graph selection asks the reader to focus, or '' for "do
 * not re-root".
 *
 * A selection is either a node id (`f:<fact>` / `s:<intent>`) or an edge id
 * (`<source>-><target>:<kind>:<intent>`). Only a node can be a root. An edge id
 * starts with its source's node id, so parsing it by prefix turned every edge
 * click into a fetch for a bogus `fact:f_…->s:…` focus root; the server has no
 * such record, and the whole map was swapped for the empty neighborhood around
 * it — a black canvas until 全图概览 was clicked.
 */
export function graphSelectionRoot(selected: string | null | undefined): string {
  // The edge id embeds its arrow; a node id never does.
  if (!selected || selected.includes('->')) return ''
  if (selected.startsWith('f:')) return `fact:${selected.slice(2)}`
  if (selected.startsWith('s:')) return `intent:${selected.slice(2)}`
  return ''
}

/**
 * Whether a deep-link root (`fact:<id>` / `intent:<id>` / `goal:<id>`) is on
 * the merged pages. Used to stop the two-page focus fetch as soon as the target
 * has been located.
 */
export function graphHasRoot(
  page: Pick<GraphRead, 'facts' | 'intents' | 'goals' | 'boundary'>,
  root: string,
): boolean {
  const sep = root.indexOf(':')
  if (sep <= 0) return root ? page.boundary.some((b) => b.id === root) : false
  const kind = root.slice(0, sep)
  const id = root.slice(sep + 1)
  if (!id) return false
  if (kind === 'fact' && page.facts.some((f) => f.id === id)) return true
  if (kind === 'intent' && page.intents.some((it) => it.id === id)) return true
  if (kind === 'goal' && page.goals.some((g) => g.id === id)) return true
  // Boundary references keep the wire form (`fact:old`), which is also a root
  // the operator can deep-link to.
  return page.boundary.some((b) => b.id === root)
}
