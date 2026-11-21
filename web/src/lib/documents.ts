// Three plain-text documents, no entries.
//
// 起点 says what is known and who is in scope, 目标 is the end state that can be
// judged, 约束 carries the one hard line and the cleanup obligation. The console
// only shows their current text, edits whole documents, and lists revisions —
// there is no per-item authorization to add, revoke or scope.
import { ApiError } from './api.ts'
import { fmtTime } from './format.ts'
import type { DocumentKind } from './types.ts'

export const DOCUMENT_KINDS: readonly DocumentKind[] = ['scope', 'goal', 'constraints']

export const DOCUMENT_META: Record<DocumentKind, { title: string; hint: string; placeholder: string }> = {
  scope: {
    title: '起点',
    hint: '资产与背景：要测什么、已知什么条件。当前这一版就是模型每轮读到的起点。',
    placeholder: '例如：\n- 授权资产：<清单>\n- 已知入口与账号清单（不含凭据）\n- 测试窗口：<时间>',
  },
  goal: {
    title: '目标',
    hint: '终点：一句能判定成立与否的话；需要验收标准时一起写在这里。',
    placeholder: '例如：拿到至少一个可复现的控制点，并留下可复核的证据。',
  },
  constraints: {
    title: '约束',
    hint: '唯一硬线与清理记录要求。只有操作员能改；模型每轮读到的是这一版原文。',
    placeholder: '约束（唯一硬线）：不要删除或修改真实数据……',
  },
}

/** One line: which revision, when, and who saved it. */
export function revisionLine(revision: { revision: number; at?: string; by?: string }): string {
  const at = revision.at ? fmtTime(revision.at) : '（时间未记录）'
  return `第 ${revision.revision} 版 · ${at} · ${revision.by || '（未记录）'}`
}

/** A 409 means someone saved after this page read the document: say so and keep
 *  the operator's draft instead of pretending the save worked. */
export function documentSaveError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'document_conflict') {
      return '文档已被改过：刷新看当前版再改，别覆盖刚保存的内容。'
    }
    if (error.status === 403) return '只有操作员能改这三份文档。'
    if (error.code === 'document_invalid') return error.message
    return error.message
  }
  return error instanceof Error ? error.message : String(error)
}
/** The save body: the new text plus the revision this page actually read. Without
 *  the second half a save silently overwrites whatever landed in between. */
export function documentSaveBody(
  doc: { revision: number },
  text: string,
): { text: string; revision: number } {
  return { text, revision: doc.revision }
}