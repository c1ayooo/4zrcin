// One 下发 box: who it goes to and what it says. That is the whole choice.
//
// The console no longer asks for a lifetime. Authorization lives in the project
// documents (起点 / 目标 / 约束) and nowhere else, so a message is never a
// boundary: a message to the commander only affects its next round, and a
// message to a direction reaches that direction (and, once the server records
// one, an escalation the commander has to answer). The receipt has to say that
// out loud — the previous "persistent instruction" path is what made an
// operator believe a sentence had changed the rules.
import { ApiError } from './api.ts'
import type {
  DirectiveRequest,
  DirectiveResponse,
  DirectiveTarget,
  Intent,
} from './types.ts'

/** A settled direction cannot receive a one-shot prompt (server 409). */
const FINISHED_STATUSES = new Set(['done', 'failed', 'parked'])

export function isDirectionFinished(status?: string): boolean {
  return Boolean(status && FINISHED_STATUSES.has(status))
}

export interface DirectiveDraft {
  text: string
  target: DirectiveTarget
  intentID: string
}

/** The exact request body. Lifetime and steer no longer exist: a directive is
 *  one durable message, never an instruction. */
export function directiveRequest(draft: DirectiveDraft, idempotencyKey: string): DirectiveRequest {
  const body: DirectiveRequest = {
    text: draft.text.trim(),
    target: draft.target,
    persistent: false,
    steer: false,
    idempotency_key: idempotencyKey,
  }
  if (draft.target === 'intent') body.intent_id = draft.intentID.trim()
  return body
}

/** Same content + same recipient = same directive; any change needs a new key. */
export function directiveFingerprint(draft: DirectiveDraft): string {
  return JSON.stringify([
    draft.text.trim(),
    draft.target,
    draft.target === 'intent' ? draft.intentID.trim() : '',
  ])
}

export interface DirectiveKeyState {
  fingerprint: string
  key: string
}

/** A fresh idempotency key for one operator request (directive or instruction). */
export function randomRequestKey(): string {
  return Array.from(crypto.getRandomValues(new Uint32Array(4)), (n) =>
    n.toString(16).padStart(8, '0'),
  ).join('')
}

/** Reuse the key for an unchanged directive so a transport retry cannot create
 *  a second row; regenerate as soon as content or recipient change. */
export function directiveKeyFor(
  state: DirectiveKeyState,
  fingerprint: string,
  generate: () => string,
): DirectiveKeyState {
  if (state.key && state.fingerprint === fingerprint) return state
  return { fingerprint, key: generate() }
}

/** Directions a message may still reach (not done/failed/parked). */
export function oneShotDirections(intents: Pick<Intent, 'id' | 'title' | 'status'>[]) {
  return intents.filter((intent) => !isDirectionFinished(intent.status))
}

const DELIVERY_TEXT: Record<string, string> = {
  queued: '已排队，对方下一次运行可见',
  sending: '投递中（不能撤回）',
  accepted: '宿主已接收（未确认已读）',
  confirmed: '对方已确认',
  unknown: '投递结果未知，不要自动重发',
}

function deliveryText(result: DirectiveResponse): string {
  return result.delivery ? (DELIVERY_TEXT[result.delivery] ?? result.delivery) : '已入队'
}

/**
 * One receipt line for the operator. Every branch says "this is not an
 * authorization": the only boundary is the project documents, so a message can
 * never be mistaken for one. When the server records an escalation the receipt
 * names it; when it does not, it says the commander was not told.
 */
export function directiveReceipt(result: DirectiveResponse): string {
  const dedupe = result.created === false ? '（重复提交，已复用同一条记录）' : ''
  if (result.target === 'commander') {
    return `已发给指挥官：${deliveryText(result)}；只影响它下一轮决策。${dedupe}`
  }
  const escalation = result.escalation
    ? `并已上报指挥官（${result.escalation.id}）。`
    : '上报回执未返回，刷新后可核对。'
  return `已送达该意图（${result.intent_id || '（未记录）'}）：${deliveryText(result)}。${escalation}${dedupe}`
}

/**
 * 409 means "that effect is unavailable now" and carries a machine code:
 * `directive_steer_unavailable` (not running/claimed) or
 * `directive_target_finished` (one-shot to a settled direction). 429 is
 * backpressure: the bytes were not dropped, so retrying the same content is safe.
 */
export function directiveErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'directive_target_finished':
        return '意图已结束：消息不能复活它；要它继续请先显式 retry/reopen 再下发。'
      case 'directive_target_missing':
        return '意图不存在：可能已被删除，刷新意图列表后重选。'
      case 'directive_invalid':
        return error.message
      case 'directive_idempotency_conflict':
        return '同一幂等键已用于不同内容：修改正文或对象后重试。'
      case 'message_backpressure':
        return '收件队列已满（message_backpressure）：服务端没有丢弃这次下发，稍后重试同一内容即可。'
    }
    if (error.status === 409) {
      return '这个动作现在不可用：已结束的意图不能收消息（先 retry/reopen 再下发）。'
    }
    if (error.status === 429) {
      return '收件队列已满（message_backpressure）：服务端没有丢弃这次下发，稍后重试同一内容即可。'
    }
    if (error.status === 404) return '意图不存在：可能已被删除，刷新意图列表后重选。'
    return error.message
  }
  return error instanceof Error ? error.message : String(error)
}

/** "在跑意图 N 个" hint for the composer footer; never a delivery guarantee. */
export function runningDirectionsHint(executors: number): string {
  return executors > 0 ? `当前在跑执行者 ${executors} 个` : '当前没有在跑的执行者'
}
