// The communication page's chronological model: one row per message, plus one
// row per instruction revision (not one per recipient).
//
// The message ledger stores an instruction change as a notification to every
// affected session. Rendering each copy would repeat the same system event N
// times; dropping them into a separate tab (the retired instruction / native IOA tabs) made
// the conversation look quieter than it was. The timeline keeps the revision
// visible once, in its real position, and the management surface behind it.
/** An instruction-change notice is one ledger row, not a chat line. Instructions
 *  are no longer created; old ledgers still carry the notices. */
function isInstructionNotice(m: { kind?: string }): boolean {
  return m.kind === 'instruction'
}import type { Message } from './types.ts'

export type TimelineEntry =
  | { kind: 'message'; seq: number; message: Message }
  | {
      kind: 'instruction'
      seq: number
      key: string
      notice: Message
      recipients: number
    }

/**
 * Build the chronological stream from the message ledger.
 *
 * - `directive_batch` is the parent of the per-executor copies; showing it
 *   would duplicate each one-shot broadcast.
 * - Instruction notices are grouped by `instruction_id@version` and collapse to
 *   the earliest seq, with the distinct-recipient count kept for the line.
 */
export function buildTimeline(rows: Message[]): TimelineEntry[] {
  const entries: TimelineEntry[] = []
  const notices = new Map<string, { notice: Message; recipients: Set<string>; seq: number }>()
  for (const message of rows) {
    if (message.kind === 'directive_batch') continue
    if (isInstructionNotice(message)) {
      const key = `${message.instruction_id || 'unknown'}@${message.instruction_version ?? 0}${
        message.instruction_id ? '' : `:${message.seq}`
      }`
      const group = notices.get(key)
      if (group) {
        group.recipients.add(message.to_participant_id)
        group.seq = Math.min(group.seq, message.seq)
      } else {
        notices.set(key, {
          notice: message,
          recipients: new Set([message.to_participant_id]),
          seq: message.seq,
        })
      }
      continue
    }
    entries.push({ kind: 'message', seq: message.seq, message })
  }
  for (const [key, group] of notices) {
    entries.push({
      kind: 'instruction',
      seq: group.seq,
      key,
      notice: group.notice,
      recipients: group.recipients.size,
    })
  }
  return entries.toSorted((a, b) => a.seq - b.seq)
}

/** The server's notice text names the revision state in full-width parens. */
export function instructionNoticeState(text: string): 'active' | 'revoked' | '' {
  const match = /（(active|revoked)）/.exec(text)
  return match ? (match[1] as 'active' | 'revoked') : ''
}
