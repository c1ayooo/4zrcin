// Presentation rules for the webshell ledger (docs/18). Pure functions so the
// view stays layout: whether a shell answers, what protocol it speaks, and how
// an operation's result reads.

import type { ShellInfoView, ShellOpResult, ShellStatus } from './types.ts'

export interface ShellStatusView {
  label: string
  tone: 'success' | 'warning' | 'destructive' | 'muted'
  dot: 'online' | 'warning' | 'pending' | 'idle' | 'error'
}

export function shellStatusView(status: ShellStatus | string): ShellStatusView {
  switch (status) {
    case 'alive':
      return { label: '存活', tone: 'success', dot: 'online' }
    case 'dead':
      return { label: '失联', tone: 'destructive', dot: 'error' }
    case 'removed':
      return { label: '已自删除', tone: 'muted', dot: 'idle' }
    case 'residual':
      return { label: '残留（无法自删除）', tone: 'warning', dot: 'warning' }
    case 'remove_failed':
      return { label: '删除失败', tone: 'destructive', dot: 'error' }
    default:
      return { label: '未确认', tone: 'muted', dot: 'pending' }
  }
}

export function protocolLabel(protocol: string): string {
  return protocol === 'v2' ? 'v2 加密' : 'legacy 明文'
}

/** An empty vantage is the console's own host (docs/18): operations default to it. */
export const LOCAL_NODE = '本机'

export function shellNodeLabel(nodeID: string | undefined): string {
  return nodeID || LOCAL_NODE
}

const OUTPUT_PREVIEW = 120

/** The one line a list row shows of a shell's last output. */
export function outputPreview(text: string | undefined): string {
  const value = (text ?? '').trim()
  if (!value) return ''
  const line = value.split('\n', 1)[0]
  return line.length > OUTPUT_PREVIEW ? `${line.slice(0, OUTPUT_PREVIEW)}…` : line
}

export function formatShellSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** Decode one file chunk: text is shown as text, binary content is only
 *  measured, never printed. */
export function decodeShellChunk(data: string): { text: string; binary: boolean; size: number } {
  const bytes = base64ToBytes(data)
  try {
    return { text: new TextDecoder('utf-8', { fatal: true }).decode(bytes), binary: false, size: bytes.length }
  } catch {
    return { text: '', binary: true, size: bytes.length }
  }
}

/** The wire carries file content as base64, so an operator typing text never
 *  has to. */
export function encodeShellWrite(text: string): string {
  return bytesToBase64(new TextEncoder().encode(text))
}

function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
  return out
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

function infoLines(info: ShellInfoView): string {
  return [
    info.os && `os: ${info.os}`,
    info.php && `php: ${info.php}`,
    info.sapi && `sapi: ${info.sapi}`,
    info.user && `user: ${info.user}`,
    info.cwd && `cwd: ${info.cwd}`,
    info.dir && `dir: ${info.dir}`,
    info.server && `server: ${info.server}`,
    info.disable_functions && `disable_functions: ${info.disable_functions}`,
    info.open_basedir && `open_basedir: ${info.open_basedir}`,
  ]
    .filter(Boolean)
    .join('\n')
}

/** One operation's result as the console shows it. */
export function describeShellResult(res: ShellOpResult): string {
  const r = res.result ?? {}
  const parts: string[] = []
  if (typeof r.output === 'string' && r.output !== '') parts.push(r.output)
  if (r.message) parts.push(r.message)
  if (r.info) parts.push(infoLines(r.info) || '（信息为空）')
  if (r.files) {
    const head = `目录 ${r.files.path} · ${r.files.entries.length} 项${r.files.truncated ? '（截断）' : ''}`
    const rows = r.files.entries.map((e) => {
      const kind = e.type === 'dir' ? 'd' : e.type === 'link' ? 'l' : '-'
      return `${kind} ${e.name}${e.type === 'dir' ? '/' : ''}${e.type === 'file' ? `  ${formatShellSize(e.size)}` : ''}`
    })
    parts.push([head, ...rows].join('\n'))
  }
  if (r.chunk) {
    const decoded = decodeShellChunk(r.chunk.data)
    parts.push(
      decoded.binary
        ? `二进制内容 ${formatShellSize(decoded.size)}（${r.chunk.path}，偏移 ${r.chunk.offset}${r.chunk.eof ? '，已到结尾' : ''}）`
        : decoded.text,
    )
  }
  if (r.written) parts.push(`已写入 ${r.written.path}：${r.written.written} 字节（文件共 ${r.written.size}）`)
  if (r.removed) parts.push('自删除已执行并验证消失')
  if (res.error) parts.push(`错误：${res.error}`)
  const text = parts.filter(Boolean).join('\n')
  return text || '（无输出）'
}
