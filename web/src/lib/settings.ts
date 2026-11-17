import type { PremisePolicy, ProjectInfo } from './types.ts'

export function nonNegativeInteger(raw: string, label: string): number {
  const value = raw.trim()
  if (!value) return 0
  if (!/^\d+$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error(`${label}必须是非负整数`)
  return Number(value)
}

export function durationLabel(seconds?: number): string {
  if (seconds == null) return '…'
  if (seconds <= 0) return '不限'
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

export type ProjectSettingsDraft = {
  /** The one-paragraph project brief / authorization boundary. */
  scope: string
  /** The constraint document (built-in defaults, editable or cleared). */
  description: string
  max_runs: string
  round_timeout: string
  verify_auto: string
  /** Stored hard-premise admission mode; there is one value. */
  premise_policy: string
}

export function projectSettingsDraft(p: ProjectInfo): ProjectSettingsDraft {
  return {
    scope: p.scope ?? '',
    description: p.description ?? '',
    max_runs: p.max_runs == null ? '' : String(p.max_runs), round_timeout: p.round_timeout ?? '',
    verify_auto: p.verify_auto == null ? '' : p.verify_auto ? 'on' : 'off',
    premise_policy: 'verified_only',
  }
}

/** Refresh clean fields while preserving each field the operator has edited. */
export function mergeProjectSettings(previous: ProjectSettingsDraft, incoming: ProjectSettingsDraft, draft: ProjectSettingsDraft): ProjectSettingsDraft {
  const next = { ...draft }
  for (const key of Object.keys(incoming) as (keyof ProjectSettingsDraft)[]) {
    if (draft[key] === previous[key]) next[key] = incoming[key]
  }
  return next
}

/** Only changed fields are sent: saving concurrency must not overwrite routing.
 *  Clearing the constraint document is an explicit change ('' is sent, meaning
 *  the project carries no description), never an omitted field. */
export function projectSettingsPatch(previous: ProjectSettingsDraft, draft: ProjectSettingsDraft) {
  const patch: { scope?: string; description?: string; max_runs?: number | null; round_timeout?: string; verify_auto?: boolean | null; premise_policy?: PremisePolicy } = {}
  if (draft.scope !== previous.scope) patch.scope = draft.scope
  if (draft.description !== previous.description) patch.description = draft.description
  if (draft.max_runs !== previous.max_runs) patch.max_runs = draft.max_runs.trim() ? nonNegativeInteger(draft.max_runs, '项目并发') : null
  if (draft.round_timeout !== previous.round_timeout) patch.round_timeout = draft.round_timeout.trim()
  if (draft.verify_auto !== previous.verify_auto) patch.verify_auto = draft.verify_auto === '' ? null : draft.verify_auto === 'on'
  if (draft.premise_policy !== previous.premise_policy) patch.premise_policy = draft.premise_policy as PremisePolicy
  return patch
}
