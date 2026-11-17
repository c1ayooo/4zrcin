import type { BoardResponse, GoalItem } from './types.ts'

/** Closing depends on the current pause and open goals, not who first paused. */
export function projectCloseoutGoal(board: BoardResponse | null | undefined): GoalItem | undefined {
  if (!board?.project_paused) return undefined
  return board.goals.open?.[0]
}

export interface ProjectPauseState {
  paused: boolean
  /** At least one goal is still open (unmet and not closed). */
  goalOpen: boolean
  /** Paused with at least one goal and nothing left to do: every goal is met or
   *  was withdrawn. All withdrawn goals are NOT a closed engagement. */
  projectClosed: boolean
  /** The commander asked to stop while a goal is still open. */
  commanderStoodBy: boolean
  /** Continue has nothing to arm; the button stays available for queued messages. */
  completionParked: boolean
}

/**
 * Banner state for the paused project (docs/01-architecture.md §3). A closed goal is neither open
 * nor met: counting it as "still unmet" would keep a withdrawn goal from ever
 * reading 已收口, so the arithmetic below subtracts it explicitly.
 */
export function projectPauseState(
  board: BoardResponse | null | undefined,
  finished: boolean,
): ProjectPauseState {
  const paused = Boolean(board?.project_paused || board?.decider?.paused)
  const goals = board?.goals
  const goalOpen = (goals?.open?.length ?? 0) > 0
  const total = goals?.total ?? 0
  const closed = goals?.closed ?? 0
  const projectClosed = paused && !goalOpen && total - closed > 0
  const commanderStoodBy = paused && board?.project_pause_kind === 'commander_standby' && goalOpen
  const completionParked =
    paused && (projectClosed || board?.project_pause_kind === 'completion_standby' || finished)
  return { paused, goalOpen, projectClosed, commanderStoodBy, completionParked }
}
