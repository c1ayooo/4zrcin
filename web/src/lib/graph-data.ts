import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type GraphRead } from './api.ts'
import {
  GRAPH_ALL_MAX_PAGES,
  GRAPH_FOCUS_MAX_PAGES,
  GRAPH_PAGE_LIMIT,
  graphHasRoot,
  graphPageKey,
  mergeGraphPages,
} from './graph-pages.ts'

// The pure page algebra also lives in graph-pages.ts (testable without React);
// re-exported here so callers that already import this module keep one entry.
export { graphPageKey, mergeGraphPages } from './graph-pages.ts'

// The graph page is a fixed snapshot: the whole ledger is read once, and new
// content is admitted only by reloading a whole snapshot (manual refresh, or the
// 实时 probe in GraphPanel), so a live write can never move the cursor
// underneath it. Pages of two versions are never merged.
//
// The map shows the whole engagement — there is no "load more" affordance to
// click — so the reader pages until the snapshot reports `has_more: false`. The
// server bounds every page by rows *and* by a byte budget, so the page count is
// data-dependent; stopping early would silently draw a partial map. The merged
// snapshot is written once at the end: calling setData per page would rebuild
// the model — and re-run the layout — while nothing is on screen to look at.
//
// Leaving the page keeps the data: only a refresh or a project switch swaps the
// snapshot.
export function useGraphSnapshot(active: boolean, project: string) {
  const [data, setData] = useState<GraphRead>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [root, setRoot] = useState('')
  const generation = useRef(0)
  const loaded = useRef(false)
  const projectRef = useRef(project)

  const write = useCallback((next: GraphRead) => {
    loaded.current = true
    setData(next)
  }, [])

  const load = useCallback(
    async (nextRoot: string) => {
      const request = ++generation.current
      setLoading(true)
      setError(undefined)
      setRoot(nextRoot)
      try {
        // A deep link (`nextRoot`) is a neighborhood, not the whole ledger: it
        // stops as soon as the root is on hand. Everything else reads to the
        // end of the snapshot.
        const maxPages = nextRoot ? GRAPH_FOCUS_MAX_PAGES : GRAPH_ALL_MAX_PAGES
        let acc: GraphRead | undefined
        let failure: unknown
        let snapshot = ''
        let cursor = ''
        let pages = 0
        for (let page = 0; page < maxPages; page++) {
          let next: GraphRead
          try {
            next = await api.graph({
              ...(nextRoot ? { root: nextRoot } : {}),
              ...(cursor ? { cursor } : {}),
              limit: GRAPH_PAGE_LIMIT,
              order: 'structural',
              projection: 'graph',
            })
          } catch (e) {
            // A mid-read failure still hands over the pages already on hand;
            // that partial snapshot is reported below, never passed off as whole.
            failure = e
            break
          }
          if (generation.current !== request) return
          const key = graphPageKey(next)
          if (page === 0) snapshot = key
          // The snapshot moved between pages: keep the consistent prefix.
          else if (key !== snapshot) break
          acc = acc ? mergeGraphPages(acc, next) : next
          pages += 1
          if (!next.has_more) break
          if (nextRoot && graphHasRoot(acc, nextRoot)) break
          cursor = next.next_cursor ?? ''
          if (!cursor) break
        }
        if (generation.current !== request) return
        if (!acc) throw failure ?? new Error('graph snapshot is empty')
        write(acc)
        // Honest partial reads: a page that failed, a snapshot that moved
        // between pages, or a ledger so large the page budget ran out all leave
        // `has_more` set. Say it, so the node counter ("快照已载 x/y") and the map
        // never disagree silently. A deep-link focus is partial by design (it is
        // one neighborhood), so it is not an error.
        if (failure) setError(failure instanceof Error ? failure.message : String(failure))
        else if (!nextRoot && acc.has_more) setError(`快照未读完：已载入 ${pages} 页，刷新可重试`)
      } catch (e) {
        if (generation.current === request) {
          setError(e instanceof Error ? e.message : String(e))
        }
      } finally {
        if (generation.current === request) {
          setLoading(false)
        }
      }
    },
    [write],
  )

  useEffect(() => {
    const switched = projectRef.current !== project
    if (switched) {
      // A different project is a different ledger: drop the old snapshot even
      // while the page is inactive, so the first activation cannot show it.
      projectRef.current = project
      generation.current++
      loaded.current = false
      setData(undefined)
      setRoot('')
      setError(undefined)
    }
    if (!active) {
      // Leaving the page keeps the snapshot; only the loading flag settles.
      setLoading(false)
      return
    }
    if (switched || !loaded.current) void load('')
  }, [active, project, load])

  useEffect(() => () => { generation.current++ }, [])

  const reload = useCallback(() => {
    void load(root)
  }, [load, root])

  return { data, loading, error, root, reload, focus: load }
}
