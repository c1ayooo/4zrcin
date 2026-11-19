// The 通信 page's aligned projection: the ledger message chain and the raw IOA
// observation chain are joined by identity, not by position or guesswork. These
// tests pin the join keys, the retry grouping, and the reasons the page gives
// when one chain has no row in the other.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { alignRecords, evidenceGapReason } from '../src/lib/alignment.ts'
import type { IOARecord, Message } from '../src/lib/types.ts'

function message(patch: Partial<Message> & { id: string; seq: number }): Message {
  return {
    project_id: 'fixture',
    from_participant_id: 'commander',
    to_participant_id: 'pt_in_a',
    text: '正文',
    status: 'accepted',
    created_at: '2026-11-19T00:00:00Z',
    updated_at: '2026-11-19T00:00:00Z',
    ...patch,
  }
}

function record(seq: number, text: string, patch: Partial<IOARecord> = {}): IOARecord {
  return {
    seq,
    ioa_message_id: `ioa_${seq}`,
    sender: 'cd3ecb7f657f17e27aef96ff92cf9312',
    observed_at: `2026-11-19T00:00:0${Math.min(seq, 9)}Z`,
    content: { text },
    ...patch,
  }
}

test('the header message_id joins a raw record to its ledger row', () => {
  const ledger = message({ id: 'm_1', seq: 1 })
  const raw = record(1, '[消息] from=operator kind=say message_id=m_1\n正文')
  const alignment = alignRecords([ledger], [raw])
  assert.deepEqual(
    alignment.byMessage.get('m_1')?.map((r) => r.seq),
    [1],
  )
  assert.equal(alignment.unaligned.length, 0)
})

test('the ledger delivery_id joins a record whose header was already stripped', () => {
  // The transport id the ledger recorded is the same IOA message id the observer
  // stored, so this is an identity join even without a message_id in the text.
  const ledger = message({ id: 'm_2', seq: 2, status: 'accepted', delivery_path: 'ioa', delivery_id: 'ioa_9' })
  const raw = record(9, '没有寻址头的原始投递')
  const alignment = alignRecords([ledger], [raw])
  assert.equal(alignment.byMessage.get('m_2')?.length, 1)
  assert.equal(alignment.unaligned.length, 0)
})

test('a retried message groups its deliveries under one row, oldest first', () => {
  const ledger = message({ id: 'm_3', seq: 3, redeliveries: 1 })
  const first = record(4, '[重投] [消息] from=commander message_id=m_3\n正文', {
    observed_at: '2026-11-19T00:00:04Z',
  })
  const second = record(5, '[消息] from=commander message_id=m_3\n正文', {
    observed_at: '2026-11-19T00:00:05Z',
  })
  const alignment = alignRecords([ledger], [second, first])
  assert.deepEqual(
    alignment.byMessage.get('m_3')?.map((r) => r.seq),
    [4, 5],
  )
})

test('the same delivery observed twice is not listed twice', () => {
  const ledger = message({ id: 'm_4', seq: 4 })
  const raw = record(6, '[collab] from=commander to=pt_in_a message_id=m_4\n正文')
  const duplicate = { ...raw }
  const alignment = alignRecords([ledger], [raw, duplicate])
  assert.equal(alignment.byMessage.get('m_4')?.length, 1)
})

test('records without a ledger row keep an explicit reason', () => {
  const ledger = message({ id: 'm_5', seq: 5 })
  const protocol = record(1, '{"event":"subagent.delegate"}')
  const noMessageId = record(2, '[collab] from=commander to=pt_in_a\n正文')
  const oldMessage = record(3, '[消息] from=operator message_id=m_old\n正文')
  const alignment = alignRecords([ledger], [protocol, noMessageId, oldMessage])

  assert.deepEqual(
    alignment.unaligned.map((entry) => entry.reason),
    ['no-header', 'header-without-message-id', 'message-not-in-window'],
  )
})

test('a record for a row outside the message window is unaligned, not dropped', () => {
  // The page only carries the last 200 ledger rows; an older observation must
  // stay visible as "not in this window" instead of silently disappearing.
  const recent = message({ id: 'm_6', seq: 6 })
  const old = record(7, '[消息] from=operator message_id=m_0\n旧正文')
  const alignment = alignRecords([recent], [old])
  assert.equal(alignment.byMessage.size, 0)
  assert.deepEqual(
    alignment.unaligned.map((entry) => entry.reason),
    ['message-not-in-window'],
  )
})

test('a gap reason is the ledger state, not a guess about the observer', () => {
  assert.equal(evidenceGapReason(message({ id: 'm_7', seq: 7, status: 'queued' })), '排队中，尚未投递')
  assert.equal(
    evidenceGapReason(message({ id: 'm_8', seq: 8, status: 'accepted', delivery_path: 'prompt', delivery_id: 'prompt:r_1' })),
    '随下一轮运行提示词投递，不经 IOA',
  )
  assert.equal(evidenceGapReason(message({ id: 'm_9', seq: 9, status: 'sending', delivery_path: 'ioa' })), 'IOA 投递中，尚未观察到')
  assert.equal(evidenceGapReason(message({ id: 'm_10', seq: 10, status: 'unknown', delivery_path: 'ioa' })), 'IOA 投递结果未知，未观察到')
  assert.match(
    evidenceGapReason(message({ id: 'm_11', seq: 11, status: 'accepted', delivery_path: 'ioa', delivery_id: 'ioa_1' })),
    /没对上/,
  )
  assert.equal(evidenceGapReason(message({ id: 'm_12', seq: 12 })), '账本没有投递路径记录')
})
