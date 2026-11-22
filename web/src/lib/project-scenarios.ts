// Scenario shapes for the 新建项目 dialog.
//
// A project already carries two texts: 起点 (the brief: what is authorised and
// what is known) and 终点 (the single endpoint goal created with it). Picking a
// scenario fills exactly those two from an existing goal preset, so starting a
// penetration engagement no longer means "create the project, then create a
// goal, then pick a template".
//
// What a scenario must not do, and why: it is a starting text, not a policy. It
// never writes or rewrites 内置约束 (that document belongs to whoever runs the
// engagement), never widens the authorisation written in 起点, and never changes
// pause/resume semantics.
import { GOAL_PRESETS, type GoalPreset } from './goal-presets.ts'

export interface ProjectScenario {
  id: string
  /** Short name shown on the chip. */
  label: string
  /** One line shown when the chip is the one currently filling the form. */
  hint: string
  /** Goal preset whose 起点 (start) and 终点 (statement) fill the two fields. */
  presetID: string
  /**
   * Whether creating from this scenario keeps the built-in constraint document.
   * `none` means the creation request carries an empty description: a benchmark
   * task has no operator-authored boundary to pre-fill.
   */
  constraints: 'default' | 'none'
}

export const PROJECT_SCENARIOS: readonly ProjectScenario[] = [
  {
    id: 'red-team',
    label: '红队',
    hint: '按「取得初始立足点」填入起点与终点：以拿到实际控制为目标，会建立会话。',
    presetID: 'foothold',
    constraints: 'default',
  },
  {
    id: 'daily-pentest',
    label: '日常渗透',
    hint: '按「广覆盖 · 停在拿 Shell 前」填入起点与终点：要覆盖面与有价值的漏洞，不要求 Shell。',
    presetID: 'broad-coverage-hold',
    constraints: 'default',
  },
]

/** The preset a scenario fills from; undefined when the library lost that id. */
export function scenarioPreset(scenario: ProjectScenario): GoalPreset | undefined {
  return GOAL_PRESETS.find((preset) => preset.id === scenario.presetID)
}
