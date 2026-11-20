// Which chain view a selection opens in.
//
// The lit closure is bounded: a hub's closure is most of the ledger (the project
// brief is 463 of 467 nodes in the sampled project), so highlighting it whole
// would light the map and answer nothing — the 197/282 failure. Past the cap a
// node therefore opens on its main path, and the strip's 看完整 stays one click
// away. Pure so the rule is testable without a canvas.

/** Closure highlight cap: how many nodes 看完整 may light. */
export const CHAIN_CAP = 200

export type ChainMode = 'closure' | 'path'

/**
 * The first view for a node: its closure while that fits under the cap, its main
 * path otherwise. Only the *first* view — an explicit 看完整 afterwards is the
 * operator's choice and is never overridden.
 */
export function defaultChainMode(total: number): ChainMode {
  return total > CHAIN_CAP ? 'path' : 'closure'
}
