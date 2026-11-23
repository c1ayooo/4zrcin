import type { AuthorizationRequest } from './types.ts'

/**
 * Two of a request's fields can disagree, and the console must show both
 * instead of picking a winner.
 *
 *  - `status` is the control plane's record (pending / resolved / dismissed);
 *    `resolution` and `resolved_at` are what the operator's own action left.
 *    "已驳回" with "工作已恢复" is a real combination: the request was dismissed
 *    and the work it asked about resumed anyway.
 *  - the request body is written by the commander, not by the control plane, so
 *    it can describe a state of its own (a 【…】 prefix saying the report was
 *    delivered). A pending row whose body says "已交付" is not a bug in the
 *    panel — it is the two fields saying different things, and the operator
 *    needs to see the disagreement to judge it.
 *
 * This module only *reports* the overlap. It never resolves it: which of the
 * two is right is a decision, and the console is not the decider.
 */
export interface AuthorizationOverlap {
  /** Status in operator words. */
  statusLabel: string
  /** The operator's resolution note, when the status is no longer pending. */
  resolution?: string
  /** A bracketed status the body itself carries, e.g. 【报告已交付（20:50）】. */
  bodyState?: string
  /** True when status still says "等你决定" but the body claims it was handled. */
  contradicts: boolean
}

const STATUS_LABEL: Record<string, string> = {
  pending: '等你决定',
  resolved: '已处理',
  dismissed: '已驳回',
}

/** The body's own 【…】 status marker, if it starts with one. */
function bodyStateOf(action: string | undefined): string | undefined {
  const text = (action ?? '').trim()
  if (!text.startsWith('【')) return undefined
  const end = text.indexOf('】')
  if (end <= 1) return undefined
  return text.slice(0, end + 1)
}

export function authorizationOverlap(
  request: Pick<AuthorizationRequest, 'status' | 'resolution' | 'resolved_at' | 'action'>,
): AuthorizationOverlap {
  const status = String(request.status ?? '')
  const statusLabel = STATUS_LABEL[status] ?? (status || '状态未记录')
  const resolution = request.resolution?.trim() || undefined
  const resolved = Boolean(request.resolved_at)
  const bodyState = bodyStateOf(request.action)
  // A pending row whose own text says the thing is done, or a closed row whose
  // resolution says the work is running again: both are "two answers at once".
  const contradicts =
    (status === 'pending' && Boolean(bodyState)) ||
    (status !== 'pending' && Boolean(resolution) && Boolean(resolved) && status !== 'resolved')
  return {
    statusLabel,
    ...(status !== 'pending' && resolution ? { resolution } : {}),
    ...(bodyState ? { bodyState } : {}),
    contradicts,
  }
}

/** One sentence naming both sides; '' when there is nothing to name. */
export function authorizationOverlapNote(overlap: AuthorizationOverlap): string {
  if (!overlap.contradicts) return ''
  if (overlap.resolution && overlap.statusLabel === '已驳回') {
    return `状态是「${overlap.statusLabel}」，但你的处理记录是「${overlap.resolution}」：请求被驳回了，相关工作又恢复了。两件事都成立，按需要重新提请求或跟进那条工作。`
  }
  if (overlap.bodyState) {
    return `正文自带状态「${overlap.bodyState}」，而控制台记录仍是「${overlap.statusLabel}」：正文由指挥官写入，状态由平台记录，两者不一致时以状态为准，但请核对这件事是否真的已经发生。`
  }
  return `状态是「${overlap.statusLabel}」，处理记录是「${overlap.resolution ?? '未记录'}」：两者可能指向不同阶段，按现状判断。`
}
