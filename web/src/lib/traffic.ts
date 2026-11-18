import type { HTTPFlowRecord } from './types.ts'

/** Decode the native protobuf bytes field. Render text only; binary stays hex. */
export function bodyPreview(base64 = '', limit = 65536): {text: string; size: number; truncated: boolean; binary: boolean} {
  const bytes = Uint8Array.from(atob(base64), c => c.charCodeAt(0))
  const prefix = bytes.subarray(0, limit)
  try {
    // Streaming permits a UTF-8 character to straddle the preview boundary.
    const text = new TextDecoder('utf-8', {fatal:true}).decode(prefix, {stream:bytes.length > limit})
    if (text.includes('\u0000')) throw new Error('binary')
    return {text, size:bytes.length, truncated:bytes.length > limit, binary:false}
  } catch {
    return {text:Array.from(prefix, b => b.toString(16).padStart(2, '0')).join(' '), size:bytes.length, truncated:bytes.length > limit, binary:true}
  }
}

export interface CapturePreviewFlags {
  /** The scan node never persisted the body bytes: what we show is a preview. */
  previewOnly: boolean
  /** The stored/forwarded body is shorter than the observed one. */
  truncated: boolean
  /** Either flag: the detail is not the exchange's original bytes. */
  partial: boolean
}

/**
 * The two body-completeness flags of one capture. The server derives them from
 * the harness's own Flow.Error prose and re-derives them on every read; a legacy
 * index row (whose JSON predates the fields) is reconciled from the preserved
 * error text here too, so an old capture is labelled honestly rather than shown
 * as complete.
 */
export function capturePreviewFlags(
  record: Pick<HTTPFlowRecord, 'preview_only' | 'truncated'> & { error?: string },
): CapturePreviewFlags {
  const error = (record.error ?? '').toLowerCase()
  const previewOnly = record.preview_only === true || error.includes('preview only')
  const truncated = record.truncated === true || error.includes('body truncated')
  return { previewOnly, truncated, partial: previewOnly || truncated }
}

/** One-line badge text for a partial capture ('' when the bytes are whole). */
export function capturePreviewBadge(flags: CapturePreviewFlags): string {
  if (flags.previewOnly && flags.truncated) return '仅预览 · 已截断'
  if (flags.previewOnly) return '仅预览'
  if (flags.truncated) return '已截断'
  return ''
}

/**
 * Operator copy for the detail pane: the difference between "the scan node kept
 * only a preview" and "the stored body was cut short" matters when a finding is
 * going to be cited, so both sentences say what is missing and what would make
 * it complete.
 */
export function capturePreviewNote(flags: CapturePreviewFlags): string {
  const notes: string[] = []
  if (flags.previewOnly) {
    notes.push(
      '仅预览：扫描节点没有保存这次交换的完整正文（traffic.body_storage 不是 disk）。默认只生成最多 4 KiB 的预览；在 cyber.yaml 开启 traffic.body_storage=disk 后才会保留完整正文。',
    )
  }
  if (flags.truncated) {
    notes.push(
      '已截断：保存的正文比实际观测到的短（受单条 8 MiB 上限或磁盘预算限制）。不要把这里的字节数当作完整响应长度；需要原文时请在宿主侧调整 traffic 限制后重放。',
    )
  }
  return notes.join(' ')
}

/**
 * The project body-retention budget is exhausted (Capture.quota_exceeded): the
 * scan keeps capturing metadata, but no new body bytes. It is a project-level
 * state, not a per-capture error, and it must never be read as "the old evidence
 * was deleted" — retention only stops growing.
 */
export function captureQuotaExceeded(record: Pick<HTTPFlowRecord, 'quota_exceeded'>): boolean {
  return record.quota_exceeded === true
}

export const captureQuotaBadge = '项目正文配额已满（仅元数据）'

export const captureQuotaNote =
  '项目正文配额已满：项目级正文预算（默认 2 GiB）用尽后，新的抓包只保留方法、URL、状态码、字节数等元数据，不再保存正文。超限不会删除已有证据或旧正文，只是不再增长；需要正文时可清理项目 .http 目录或调整 traffic.body_retention_bytes 后重放。'
