// 操作员指令: versioned operator guidance that is injected into every later
// dispatch. This file owns the wording and the request/response mapping; the
// component only draws it.
//
// The surface exists because the one-shot 下发 composer must stay one-shot: a
// hint once sent there was silently turned into a standing rule and polluted
// every later dispatch. A persistent directive is the same sentence with a
// different lifetime, so it gets its own explicit create action and its own
// vocabulary — "guidance", never "authorization". Boundaries live in the three
// project documents alone.
import { ApiError } from './api.ts'
import { directiveKeyFor, randomRequestKey, type DirectiveKeyState } from './directive.ts'
import { fmtTime } from './format.ts'
import type {
  DirectiveRequest,
  DirectiveResponse,
  Instruction,
  InstructionHistoryResponse,
  Intent,
} from './types.ts'

/** The server's own limit (store.MaxInstructionRunes); mirrored so the counter
 *  and the server refusal cannot disagree. */
export const MAX_INSTRUCTION_RUNES = 4000

/** The whole surface says this on every render: guidance, not a boundary. */
export const INSTRUCTION_GUIDANCE =
  '持续注入指挥官与执行者的每一轮任务书。它是指导，不是授权边界；授权、范围与停手线只在项目三份文档里。'

export const INSTRUCTION_EMPTY =
  '还没有操作员指令。想影响下一轮用通信页的「下发」；要以后每轮都生效，才建指令。'

export type InstructionScope = 'project' | 'intent'

export interface InstructionDraft {
  text: string
  scope: InstructionScope
  intentID: string
}

/** Same content + same scope = same request; a retried submit must not create a
 *  second instruction row. */
export function instructionFingerprint(draft: InstructionDraft): string {
  return JSON.stringify([
    draft.text.trim(),
    draft.scope,
    draft.scope === 'intent' ? draft.intentID.trim() : '',
  ])
}

/** Reuse the key for an unchanged draft; regenerate as soon as anything moves. */
export function instructionKeyFor(
  state: DirectiveKeyState,
  fingerprint: string,
  generate: () => string = randomRequestKey,
): DirectiveKeyState {
  return directiveKeyFor(state, fingerprint, generate)
}

/** What the create form must refuse locally, before the server sees it. */
export function instructionDraftError(draft: InstructionDraft): string | undefined {
  const text = draft.text.trim()
  if (!text) return '写一条指令再提交。'
  if ([...text].length > MAX_INSTRUCTION_RUNES) return `指令最多 ${MAX_INSTRUCTION_RUNES} 字。`
  if (draft.scope === 'intent' && !draft.intentID.trim()) return '选一个意图，或把作用范围改成整个项目。'
  return undefined
}

/** POST /api/directives: persistent=true is what makes this an instruction
 *  rather than a message; the server's validation keeps the two apart. */
export function instructionCreateBody(draft: InstructionDraft, idempotencyKey: string): DirectiveRequest {
  const body: DirectiveRequest = {
    text: draft.text.trim(),
    target: draft.scope,
    persistent: true,
    steer: false,
    idempotency_key: idempotencyKey,
  }
  if (draft.scope === 'intent') body.intent_id = draft.intentID.trim()
  return body
}

export function instructionCreateError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'directive_invalid') return error.message
    if (error.code === 'directive_target_missing') return '这个意图不在了：刷新后再选。'
    if (error.status === 403) return '只有操作员能下发指令。'
    return error.message
  }
  return error instanceof Error ? error.message : String(error)
}

/** Revoking is an update: the version and text this page read travel back, so a
 *  row changed in another tab is refused instead of silently overwritten. */
export function instructionRevokeBody(instruction: Instruction): {
  version: number
  text: string
  state: 'revoked'
} {
  return { version: instruction.version, text: instruction.text, state: 'revoked' }
}

export function instructionRevokeError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 409) return '这条指令已被改过（版本冲突）：刷新后再撤销。'
    if (error.status === 403) return '只有操作员能撤销指令。'
    return error.message
  }
  return error instanceof Error ? error.message : String(error)
}

export function instructionStateLabel(state: Instruction['state']): string {
  return state === 'revoked' ? '已撤销' : '生效中'
}

/** "整个项目" or the direction's title; an id the ledger cannot title stays an
 *  id instead of an empty quote. */
export function instructionScopeLabel(
  instruction: Instruction,
  titles: ReadonlyMap<string, string> | Record<string, string> = {},
): string {
  if (!instruction.intent_id) return '整个项目'
  const title = titles instanceof Map
    ? titles.get(instruction.intent_id)
    : (titles as Record<string, string>)[instruction.intent_id]
  return title ? `意图「${title}」` : `意图 ${instruction.intent_id}`
}

/** When the row last changed, in the one format the console uses everywhere. */
export function instructionTimeLine(instruction: Instruction): string {
  return `v${instruction.version} · ${fmtTime(instruction.updated_at || instruction.created_at)}`
}

/** Active first, newest first inside each group: revoked rows are history and
 *  must not push the standing set down. */
export function orderInstructions(rows: readonly Instruction[]): Instruction[] {
  const at = (row: Instruction) => row.updated_at || row.created_at || ''
  return [...rows].sort((a, b) => {
    const state = Number(a.state === 'revoked') - Number(b.state === 'revoked')
    if (state !== 0) return state
    return at(b).localeCompare(at(a))
  })
}

export function activeInstructionCount(rows: readonly Instruction[]): number {
  return rows.filter((row) => row.state !== 'revoked').length
}

export interface InstructionRevision {
  version: number
  operation: string
  at?: string
  state: Instruction['state']
  text: string
}

const OPERATION_LABEL: Record<string, string> = {
  create: '新建',
  update: '修订',
  baseline: '初始',
  move: '位置变化',
}

export function instructionOperationLabel(operation?: string): string {
  if (!operation) return '修订'
  return OPERATION_LABEL[operation] ?? operation
}

/** Turn raw change rows into what the history list draws. `value` is the
 *  instruction at that revision, so its text/version/state are the revision's;
 *  the change row's own version is only a fallback. */
export function instructionRevisions(
  page: InstructionHistoryResponse | null | undefined,
): InstructionRevision[] {
  return (page?.history ?? [])
    .map((change) => ({
      version: change.value?.version ?? change.version,
      operation: instructionOperationLabel(change.operation),
      at: change.at,
      state: change.value?.state ?? 'active',
      text: change.value?.text ?? '',
    }))
    .sort((a, b) => b.version - a.version)
}

export function instructionCreatedReceipt(result: DirectiveResponse): string {
  const scope = result.instruction?.intent_id ? '该意图及其后代' : '整个项目'
  const dedupe = result.created === false ? '（重复提交，已复用同一条指令）' : ''
  return `已生效：${scope}，之后每轮注入。${dedupe}`
}

export function instructionRevokedReceipt(instruction: Instruction): string {
  return `已撤销 v${instruction.version}：之后不再注入，历史保留。`
}

/** The direction picker shows every intent: a persistent instruction may scope
 *  to a finished direction too (it applies to the retry/continue descendants). */
export function instructionIntentOptions(
  intents: readonly Pick<Intent, 'id' | 'title' | 'status'>[],
): Pick<Intent, 'id' | 'title' | 'status'>[] {
  return intents.map((intent) => ({ id: intent.id, title: intent.title, status: intent.status }))
}
