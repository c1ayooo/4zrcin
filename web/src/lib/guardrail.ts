// Tool-approval (guardrail/JEV) presentation helpers.
//
// The server relays the host's questions; this module only turns the read
// projection into operator language. It deliberately keeps "unavailable" and
// "never checked" out of the error vocabulary: no guardrail configured is a
// configuration state, not a failure the operator has to fix in this pane.

import type {
  Event,
  GuardrailPendingItem,
  GuardrailResolveResult,
  OperatorInboxGuardrail,
} from './types.ts'

export type GuardrailStateTone = 'success' | 'warning' | 'muted' | 'info' | 'outline'

/** Short state label for a badge. Availability with a zero count is valid. */
export function guardrailStateLabel(state: string | undefined, count = 0): string {
  switch (state) {
    case 'available':
      return count > 0 ? `${count} 项待审批` : '可审批，暂无待审批'
    case 'unavailable':
      // The reason below says whether it is "no JEV", "session ended" or "host
      // down"; the label stays one word so the badge does not become a paragraph.
      // One vocabulary across the rail badge and this card: 不可用 means asked and
      // got no usable answer, which is not the same as 「没有待审批」.
      return '不可用'
    case 'never_checked':
    case '':
    case undefined:
      return '未查询'
    default:
      return state
  }
}

/** The reason line under the state. Falls back to what the state means. */
export function guardrailStateReason(
  summary: { state?: string; reason?: string } | undefined,
): string {
  if (summary?.reason?.trim()) return summary.reason.trim()
  switch (summary?.state) {
    case 'available':
      return '宿主可回答工具审批；当前没有拦截中的工具调用。'
    case 'unavailable':
      return '宿主没有启用工具审批（guardrail/JEV），或当前没有可读取的会话。'
    case 'never_checked':
    case undefined:
      return '还没有读取过工具审批；展开或刷新后会向宿主查询一次。'
    default:
      return ''
  }
}

/** Badge variant for the state: never red — this is not a failed request. */
export function guardrailStateVariant(
  state: string | undefined,
  count = 0,
): 'success' | 'warning' | 'muted' | 'info' | 'outline' {
  switch (state) {
    case 'available':
      return count > 0 ? 'warning' : 'success'
    case 'unavailable':
      return 'muted'
    case 'never_checked':
    case '':
    case undefined:
      return 'outline'
    default:
      return 'info'
  }
}

/** A pending row's human title: the tool name, else its kind, else unknown. */
export function guardrailItemTitle(item: GuardrailPendingItem): string {
  return item.tool?.trim() || item.kind?.trim() || '未命名工具调用'
}

/**
 * The arguments, rendered for reading. The server relays valid provider JSON
 * verbatim and clips undecodable bytes into arguments_text; both shapes are
 * shown as text and clipped again so one huge payload cannot own the panel.
 */
export function guardrailArgumentsText(item: GuardrailPendingItem, limit = 1200): string {
  let text = ''
  if (item.arguments !== undefined && item.arguments !== null) {
    try {
      text = JSON.stringify(item.arguments, null, 2)
    } catch {
      text = String(item.arguments)
    }
  } else if (item.arguments_text?.trim()) {
    text = item.arguments_text.trim()
  }
  if (!text.trim()) return '（无参数）'
  return text.length > limit ? `${text.slice(0, limit)}…` : text
}

/**
 * The expiry the operator must beat. `known: false` means the host sent no
 * deadline (nothing to count down); an invalid date is reported as unknown, not
 * as "expired", because that would claim the host refused it.
 */
export function guardrailExpiry(
  expiresAt: string | undefined,
  now = Date.now(),
): { label: string; expired: boolean; known: boolean } {
  if (!expiresAt) return { label: '无到期时间', expired: false, known: false }
  const at = Date.parse(expiresAt)
  if (!Number.isFinite(at)) return { label: '到期时间无效', expired: false, known: false }
  const remaining = at - now
  if (remaining <= 0) return { label: '已过期', expired: true, known: true }
  const seconds = Math.ceil(remaining / 1000)
  if (seconds < 60) return { label: `剩余 ${seconds}s`, expired: false, known: true }
  const minutes = Math.ceil(remaining / 60_000)
  if (minutes < 60) return { label: `剩余 ${minutes} 分钟`, expired: false, known: true }
  return { label: `剩余 ${Math.ceil(remaining / 3_600_000)} 小时`, expired: false, known: true }
}

/** One row's stable key: operation id is unique within a session. */
export function guardrailItemKey(item: GuardrailPendingItem): string {
  return `${item.session_id}/${item.operation_id}`
}

/**
 * Feedback after POST /api/guardrail/resolve. A 409 "failed"/"unavailable" is a
 * host answer with its own reason: it must be shown, not folded into a generic
 * transport error.
 */
export function guardrailResolveNotice(result: GuardrailResolveResult): {
  tone: 'success' | 'warning' | 'destructive'
  text: string
} {
  const action = result.approve ? '批准' : '拒绝'
  switch (result.state) {
    case 'resolved':
      return { tone: 'success', text: `已${action}该工具调用。宿主已解除拦截，会话会继续本轮。` }
    case 'failed':
      return {
        tone: 'warning',
        text: `宿主没有接受这次${action}：${result.reason?.trim() || '可能已过期或已由别处处理'}`,
      }
    case 'unavailable':
      return { tone: 'warning', text: `工具审批暂不可用：${result.reason?.trim() || '宿主或 guardrail 命名空间不可达'}` }
    default:
      return { tone: 'warning', text: `审批结果未确认（state=${result.state}）${result.reason ? `：${result.reason}` : ''}` }
  }
}

/** Session id shortened for a dense row, with the full id in the title. */
export function guardrailSessionShort(sessionID: string): string {
  return sessionID.length > 12 ? `${sessionID.slice(0, 12)}…` : sessionID
}

// ---- guardrail_decision / guardrail_review run events ----
//
// The journal also records the policy's own verdicts, not only pending tool
// calls: kind guardrail_decision (what policy saw/did) and guardrail_review
// (an intercepted call awaiting the operator). The structured fields are
// primary; the status Text (`guardrail block bash：原因`) is parsed as a fallback
// so a journal row rendered before/without the extra fields is still labelled.

export type GuardrailAction = 'record' | 'review' | 'block' | 'unknown'

export interface GuardrailEventView {
  action: GuardrailAction
  tool: string
  reason: string
  state?: string
  operationID?: string
  callID?: string
}

const GUARDRAIL_EVENT_KINDS = new Set(['guardrail_decision', 'guardrail_review'])
const guardrailTextPattern = /^guardrail\s+(record|review|block)\b\s*(.*)$/i

/** Parse one guardrail journal event; null for every other kind. */
export function parseGuardrailEvent(event: Event): GuardrailEventView | null {
  if (!GUARDRAIL_EVENT_KINDS.has(event.kind)) return null
  const text = (event.text ?? '').trim()
  const textMatch = guardrailTextPattern.exec(text)
  const rawAction = (event.guardrail_action ?? textMatch?.[1] ?? '').toLowerCase()
  const action: GuardrailAction =
    rawAction === 'record' || rawAction === 'review' || rawAction === 'block' ? rawAction : 'unknown'
  // Text shape: `guardrail block bash：原因` — tool then an optional ：reason.
  let tool = event.guardrail_tool?.trim() ?? ''
  let reason = event.guardrail_reason?.trim() ?? ''
  if (textMatch) {
    const body = textMatch[2].trim()
    if (!tool || !reason) {
      const split = /^([^\s：:]+)\s*(?:[：:]\s*(.*))?$/.exec(body)
      if (split) {
        if (!tool) tool = split[1]
        if (!reason) reason = split[2]?.trim() ?? ''
      } else if (!reason) {
        reason = body
      }
    }
  }
  if (!reason && !tool && !textMatch) reason = text
  const view: GuardrailEventView = { action, tool, reason }
  if (event.guardrail_state?.trim()) view.state = event.guardrail_state.trim()
  if (event.guardrail_operation_id?.trim()) view.operationID = event.guardrail_operation_id.trim()
  if (event.guardrail_call_id?.trim()) view.callID = event.guardrail_call_id.trim()
  return view
}

/** record = quiet fact, review = needs attention, block = refused. */
export function guardrailActionVariant(action: GuardrailAction): 'muted' | 'warning' | 'danger' {
  switch (action) {
    case 'block':
      return 'danger'
    case 'review':
      return 'warning'
    default:
      return 'muted'
  }
}

export function guardrailActionLabel(action: GuardrailAction): string {
  switch (action) {
    case 'record':
      return '记录'
    case 'review':
      return '复核'
    case 'block':
      return '阻断'
    default:
      return '未标注动作'
  }
}

/**
 * Index of the journal event that carries one provider call id, or -1.
 *
 * The same call is written in two shapes: a tool call/result keeps it in
 * `call_id`, a guardrail decision keeps it in `guardrail_call_id`. Both name
 * one call, so either is accepted; anything else is not a near match and the
 * caller must report "not found" instead of pointing at a neighbour.
 */
export function guardrailCallEventIndex(events: Event[], callID: string): number {
  const wanted = callID.trim()
  if (!wanted) return -1
  for (let i = 0; i < events.length; i++) {
    const event = events[i]
    if (event.call_id?.trim() === wanted) return i
    if (event.guardrail_call_id?.trim() === wanted) return i
  }
  return -1
}

/** The operator-facing lifecycle state of one reviewed call. */
export function guardrailEventStateLabel(state?: string): string {
  switch (state) {
    case 'pending':
      return '待审批'
    case 'approved':
      return '已批准'
    case 'rejected':
      return '已拒绝'
    case 'expired':
      return '已过期'
    case 'canceled':
      return '已取消'
    case '':
    case undefined:
      return ''
    default:
      return state
  }
}
