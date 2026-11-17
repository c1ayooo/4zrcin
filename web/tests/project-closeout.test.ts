import { test } from 'node:test'
import assert from 'node:assert/strict'
import { projectCloseoutGoal, projectPauseState } from '../src/lib/project-controls.ts'
import type { BoardResponse } from '../src/lib/types.ts'

test('closeout remains available after commander stop, accidental continue, and manual pause', () => {
  const goal = {id: 'g_open', statement: 'unfinished goal'}
  const board = {project_paused: true, project_pause_kind: 'commander_standby', goals: {total: 1, met: 0, open: [goal]}} as BoardResponse
  assert.equal(projectCloseoutGoal(board), goal)
  assert.equal(projectCloseoutGoal({...board, project_paused: false, project_pause_kind: 'running'}), undefined)
  assert.equal(projectCloseoutGoal({...board, project_pause_kind: 'manual_pause'}), goal)
  assert.equal(projectCloseoutGoal({...board, goals: {total: 1, met: 1, open: []}}), undefined)
  assert.equal(projectCloseoutGoal(undefined), undefined)
})

test('a closed goal is neither open nor met: it cannot keep the project from reading 已收口', () => {
  // One goal was completed, another was withdrawn: the project IS closed.
  const completedAndClosed = {
    project_paused: true, project_pause_kind: 'commander_standby', decider: { paused: true },
    goals: { total: 2, met: 1, closed: 1, open: [] },
  } as BoardResponse
  const state = projectPauseState(completedAndClosed, false)
  assert.equal(state.goalOpen, false)
  assert.equal(state.commanderStoodBy, false, 'a closed goal must not look like unfinished work')
  assert.equal(state.projectClosed, true)
  assert.equal(state.completionParked, true)

  // Only closed goals left: nothing was completed, so this is not a closed engagement.
  const allWithdrawn = {
    project_paused: true, project_pause_kind: 'manual_pause', decider: { paused: true },
    goals: { total: 1, met: 0, closed: 1, open: [] },
  } as BoardResponse
  const withdrawn = projectPauseState(allWithdrawn, false)
  assert.equal(withdrawn.goalOpen, false)
  assert.equal(withdrawn.projectClosed, false)
  assert.equal(withdrawn.completionParked, false)

  // An open goal dominates: the commander stop banner still applies.
  const open = {
    project_paused: true, project_pause_kind: 'commander_standby', decider: { paused: true },
    goals: { total: 3, met: 1, closed: 1, open: [{id: 'g_open', statement: 'unfinished'}] },
  } as BoardResponse
  const running = projectPauseState(open, false)
  assert.equal(running.goalOpen, true)
  assert.equal(running.commanderStoodBy, true)
  assert.equal(running.projectClosed, false)

  // Older board without the closed count keeps the previous arithmetic.
  const legacy = {
    project_paused: true, project_pause_kind: 'completion_standby', decider: { paused: true },
    goals: { total: 1, met: 1, open: [] },
  } as BoardResponse
  assert.equal(projectPauseState(legacy, false).projectClosed, true)
  // Running (not paused) is never "closed".
  assert.equal(projectPauseState({...legacy, project_paused: false, decider: {paused: false}}, false).projectClosed, false)
  assert.equal(projectPauseState(undefined, false).paused, false)
})
