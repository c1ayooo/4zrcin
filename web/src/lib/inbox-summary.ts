import type { OperatorInboxSummary } from './types.ts'

/**
 * What the rail badge says, from the backend inbox summary.
 *
 * The summary is the single read the badge, the overview card and the detail
 * list share, so the badge never counts rows of its own. Two rules shape it:
 *
 *  - a countable summary shows its total, and a total of 0 shows nothing at all
 *    (a "0" badge is noise that also reads as a count);
 *  - when any category is unknown (`countable === false`), no exact number may
 *    be printed: 未查询 and 不可用 are answers too, and a badge reading 0 because
 *    nobody asked is a lie the operator acts on. If other categories do have
 *    rows, the badge shows them as a lower bound ("2+") and the tooltip says why.
 */
export type InboxBadgeKind = 'count' | 'lower' | 'unknown'

export interface InboxBadge {
  text: string
  kind: InboxBadgeKind
  /** Long explanation for the title/aria, set only when the text is not exact. */
  title: string
}

const KIND_LABEL: Record<string, string> = {
  authorization: '授权请求',
  handoff: '交接',
  acceptance: '验收回执',
  warning: '运行告警',
  guardrail: '工具审批',
}

function unknownHeading(summary: OperatorInboxSummary): { text: InboxBadge['text']; title: string } {
  const unknown = (summary.categories ?? []).filter(
    (category) => category.state === 'never_checked' || category.state === 'unavailable',
  )
  const labels = unknown.map((category) => KIND_LABEL[category.kind] ?? category.kind)
  if (unknown.some((category) => category.state === 'unavailable')) {
    const reason = unknown.find((category) => category.state === 'unavailable')?.reason
    return {
      text: '不可用',
      title: `这些类别读不到：${labels.join('、')}${reason ? `（${reason}）` : ''}。不可用不等于没有待办。`,
    }
  }
  return {
    text: '未查询',
    title: `这些类别还没有查询过：${labels.join('、')}。未查询不等于没有待办。`,
  }
}

export function inboxBadge(summary: OperatorInboxSummary | undefined): InboxBadge | undefined {
  if (!summary) return undefined
  if (summary.countable) {
    if (summary.total <= 0) return undefined
    return { text: String(summary.total), kind: 'count', title: `待你处理 ${summary.total} 项（授权 / 交接 / 验收 / 告警 / 审批）` }
  }
  const unknown = unknownHeading(summary)
  if (summary.total > 0) {
    return {
      text: `${summary.total}+`,
      kind: 'lower',
      title: `已知 ${summary.total} 项待处理，但还有类别没读到，数字是下限。${unknown.title}`,
    }
  }
  return { text: unknown.text, kind: 'unknown', title: unknown.title }
}
