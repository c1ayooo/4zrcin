import { api } from './api.ts'
import { useLoad } from './hooks.ts'
import { classifyReviewFailure, type FactVerdicts, type ReviewFailure } from './review-failure.ts'

/**
 * Read the review rows of the facts a failed direction still owes. Ids only —
 * the caller already has them from `review_failed_facts`; an empty list means
 * the review phase never got far enough to name a fact, which is itself the
 * "unfinished" case. Kept apart from `review-failure.ts` so the derivation and
 * its wording stay importable without React or the API client.
 */
export function useReviewFailure(factIDs: string[] | undefined) {
  const ids = factIDs ?? []
  const key = ids.join(',')
  return useLoad<ReviewFailure>(async () => {
    if (ids.length === 0) return classifyReviewFailure([])
    const facts = await Promise.all(
      ids.map(async (id): Promise<FactVerdicts> => {
        try {
          const rows = await api.reviews(id)
          return { id, verdicts: rows.map((row) => row.verdict) }
        } catch {
          return { id, verdicts: null }
        }
      }),
    )
    return classifyReviewFailure(facts)
  }, [key])
}
