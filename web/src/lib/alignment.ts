// The 通信 page shows two record chains for the same collaboration:
//
//   - the ledger (`Message`) is what the control plane accepted, retried,
//     acknowledged or is still holding queued; it is the authoritative record of
//     intent and delivery state;
//   - the IOA observer's raw records are what actually crossed the live
//     transport, one row per observed delivery.
//
// Neither is a subset of the other: a queued message or a prompt-carried one
// never produces an IOA record, while the observer also sees other projects,
// harness-internal protocol events and unparseable rows. This module builds the
// one projection the console can show honestly: match each raw record to a
// ledger row by identity (message_id in the addressing header, or the ledger's
// recorded IOA delivery id), and report the rest separately instead of letting
// one chain stand in for the other.
import { parseRecordText, rawRecordKey } from './ioa.ts'
import type { IOARecord, Message } from './types'

/** Why a raw record has no ledger row in the current window. */
export type UnalignedReason = 'no-header' | 'header-without-message-id' | 'message-not-in-window'

export interface UnalignedObservation {
  record: IOARecord
  reason: UnalignedReason
}

export interface Alignment {
  /** Ledger message id → the raw records that carried it, oldest first. */
  byMessage: ReadonlyMap<string, readonly IOARecord[]>
  /** Raw records that carry no ledger row in the current message window. */
  unaligned: readonly UnalignedObservation[]
}

function recordText(rec: IOARecord): string {
  return typeof rec.content?.text === 'string' ? rec.content.text : ''
}

function observedAt(rec: IOARecord): number {
  const parsed = Date.parse(rec.created_at || rec.observed_at)
  return Number.isNaN(parsed) ? 0 : parsed
}

/**
 * Join the ledger window and the project's raw IOA records.
 *
 * A record matches a ledger row by either identity the two chains share:
 *   1. the addressing header's `message_id` (= the ledger message id), which
 *      covers peer messages and console deliveries alike;
 *   2. `rec.ioa_message_id` = the ledger row's `delivery_id`, which is the IOA
 *      message id the transport reported.
 *
 * The ledger row is looked up by `message_id` first: a retry may add another
 * record for the same message, while `delivery_id` on the ledger stays the one
 * the transport last reported.
 */
export function alignRecords(messages: readonly Message[], records: readonly IOARecord[]): Alignment {
  const byMessageID = new Map<string, Message>()
  const byDeliveryID = new Map<string, Message>()
  for (const message of messages) {
    byMessageID.set(message.id, message)
    if (message.delivery_id && !message.delivery_id.startsWith('prompt:')) {
      byDeliveryID.set(message.delivery_id, message)
    }
  }

  const byMessage = new Map<string, IOARecord[]>()
  const unaligned: UnalignedObservation[] = []
  for (const record of records) {
    const header = parseRecordText(recordText(record)).header
    const message =
      (header?.messageId ? byMessageID.get(header.messageId) : undefined) ??
      (record.ioa_message_id ? byDeliveryID.get(record.ioa_message_id) : undefined)
    if (!message) {
      unaligned.push({
        record,
        reason: !header
          ? 'no-header'
          : header.messageId
            ? 'message-not-in-window'
            : 'header-without-message-id',
      })
      continue
    }
    const bucket = byMessage.get(message.id)
    if (bucket) {
      // The same delivery can be observed by more than one host observer; the
      // server already dedupes by record key, and this keeps the projection
      // stable if that ever changes.
      if (!bucket.some((existing) => rawRecordKey(existing) === rawRecordKey(record))) bucket.push(record)
    } else {
      byMessage.set(message.id, [record])
    }
  }

  for (const bucket of byMessage.values()) {
    bucket.sort((a, b) => observedAt(a) - observedAt(b) || a.seq - b.seq)
  }
  unaligned.sort((a, b) => observedAt(a.record) - observedAt(b.record) || a.record.seq - b.record.seq)
  return { byMessage, unaligned }
}

/**
 * Why a ledger row has no observed delivery. The answer is a state of the
 * ledger record, not a guess about the observer: a queued message was never
 * sent, a prompt-carried one is not an IOA delivery at all.
 */
export function evidenceGapReason(message: Message): string {
  if (message.status === 'queued') return '排队中，尚未投递'
  if (message.delivery_path === 'prompt' || (message.delivery_id ?? '').startsWith('prompt:')) {
    return '随下一轮运行提示词投递，不经 IOA'
  }
  if (message.status === 'sending') return 'IOA 投递中，尚未观察到'
  if (message.status === 'unknown') return 'IOA 投递结果未知，未观察到'
  if (message.delivery_path === 'ioa' || message.delivery_id) {
    return '当前 500 条观察里没对上（观察可能缺行或已轮出）'
  }
  return '账本没有投递路径记录'
}
