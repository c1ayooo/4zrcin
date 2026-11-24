// The graph model hook (T6, docs/33 §4.6): a signature-memoized buildGraph over
// the ledger snapshot, with the drawing-only filters applied on the input side.
// Nothing here changes what the ledger says; filtering only decides which nodes
// are drawn. The pure filter lives in T7's lib/graph-filter.ts so the canvas and
// this hook share one definition of "hidden"; the pure cache core (signature +
// memo decision) lives in lib/graph-model-cache.ts so the F13 rules are testable
// without a renderer.

import { useMemo, useRef } from 'react'
import type { GraphModel } from '@/graph/model'
import {
  resolveGraphModelMemo,
  type GraphFiltersState,
  type GraphModelMemo,
  type GraphModelSource,
} from '@/lib/graph-model-cache'

// Re-exported for the page shell: one import path, unchanged from before the
// cache core was split out.
export { defaultGraphFilters } from '@/lib/graph-model-cache'
export type { GraphFiltersState, GraphModelSource } from '@/lib/graph-model-cache'

/**
 * Build the render model. The cheap filters (kind / time / done) are applied
 * before buildGraph so a hidden branch costs no layout, hit-test or paint work
 * at all. The selection is not an input: it only changes the highlight, never
 * which nodes exist — that is what keeps a click from re-laying the map.
 */
export function useGraphModel(source: GraphModelSource, filters: GraphFiltersState): GraphModel {
  const memoRef = useRef<GraphModelMemo | null>(null)
  const { data, project, runs, defaultModel } = source
  return useMemo(() => {
    const next = resolveGraphModelMemo(memoRef.current, source, filters, Date.now())
    memoRef.current = next
    return next.model
    // Each field describes a resource the model is a pure function of. The hook
    // intentionally mirrors the original signature-memo dependencies.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, project, runs, defaultModel, filters])
}
