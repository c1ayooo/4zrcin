import type { AuthorizationRequest, Goal, OperatorInbox } from './types.ts'

/**
 * The two inbox records that used to have no address of their own.
 *
 * A todo row names its record by id; the detail pages read the same id from the
 * URL (`?view=access&id=` / `?view=state&goal=`) and look it up here. Both
 * helpers return `undefined` when the id names nothing the current read knows —
 * a stale link must degrade to "record not found", never to the first row.
 */

export function findAuthorization(
  inbox: OperatorInbox | undefined,
  requestID: string | null | undefined,
): AuthorizationRequest | undefined {
  if (!inbox || !requestID) return undefined
  return inbox.authorization.find((request) => request.id === requestID)
}

export function findGoal(goals: Goal[] | undefined, goalID: string | null | undefined): Goal | undefined {
  if (!goals || !goalID) return undefined
  return goals.find((goal) => goal.id === goalID)
}
