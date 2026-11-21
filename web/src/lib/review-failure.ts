// A direction can end in `review_failed` for two very different reasons, and the
// operator's next move is not the same:
//
//   review_unfinished  nothing decided it — the verifier run was interrupted or
//                      died before it could write a row (e.g. a dial failure).
//                      The fact stays unverified and the queue picks it up again;
//                      retrying or waiting is the right move.
//   review_rejected    a verifier did run and said the conclusion is not proved
//                      (`needs_evidence` / `reject`). Nobody self-heals this:
//                      the conclusion has to change or the evidence has to be
//                      added.
//
// The ledger already separates them structurally — a real verdict writes a
// review row, an interrupted run writes none — so the console asks for the row
// instead of parsing the note text. The label is derived, never a new status.


export type ReviewFailureKind = 'review_rejected' | 'review_unfinished'

/** Verdicts that say "this conclusion is not proved yet". */
const REJECTING_VERDICTS = new Set(['needs_evidence', 'reject'])

export interface ReviewFailure {
  kind: ReviewFailureKind
  /** Facts whose review row rejects the current conclusion. */
  rejected: string[]
  /** Facts whose reviews could not be read: the label must not guess. */
  unread: string[]
}

/** One fact's verdicts, or null when the read failed. */
export interface FactVerdicts {
  id: string
  verdicts: string[] | null
}

/** Fold per-fact verdicts into one direction-level answer. A single rejecting
 *  verdict is enough: someone has to change that conclusion. */
export function classifyReviewFailure(facts: FactVerdicts[]): ReviewFailure {
  const rejected: string[] = []
  const unread: string[] = []
  for (const fact of facts) {
    if (fact.verdicts === null) unread.push(fact.id)
    else if (fact.verdicts.some((verdict) => REJECTING_VERDICTS.has(verdict))) rejected.push(fact.id)
  }
  return { kind: rejected.length > 0 ? 'review_rejected' : 'review_unfinished', rejected, unread }
}

/** The one phrase a surface shows for this direction's review failure. */
export function reviewFailureLabel(failure: ReviewFailure | null | undefined): string {
  if (!failure) return '复核未通过'
  if (failure.kind === 'review_rejected') return '复核判定不通过'
  // An unread review list is not "nothing ran": say which half is missing
  // instead of borrowing the reassuring label.
  if (failure.unread.length > 0) return '复核未通过（复核记录没读到）'
  return '复核没跑完（可重试）'
}

/** The sentence under the label: what to do about it, and what not to assume. */
export function reviewFailureHint(failure: ReviewFailure | null | undefined): string {
  if (!failure) return '先确认复核记录是没跑到，还是判定不通过，再决定重试还是改结论。'
  if (failure.kind === 'review_rejected') {
    return '复核给出了否定结论：这些事实要改结论或补证据，重试不会自动通过。'
  }
  if (failure.unread.length > 0) return '复核记录没读到，别按「判定不通过」处理；刷新后再决定。'
  return '复核没跑完，不代表结论被否：可重试，或等自动复核队列重跑。'
}
