import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import {
  evaluatorBadgeVariant,
  evaluatorRoundText,
  evaluatorSummaryText,
  evaluatorVerdictLabel,
  latestEvaluatorResult,
  parseEvaluatorEvent,
  parseEvaluatorResults,
} from '../src/lib/run-evaluator.ts'
import type { Event } from '../src/lib/types.ts'

function status(state: string, text: string): Event {
  return { kind: 'status', state, text, at: '2026-11-18T00:00:00Z' }
}

test('a passing eval_end status parses verdict, round and reason', () => {
  const parsed = parseEvaluatorEvent(status('eval_end', 'eval_end 第2/3轮：通过 找到可复现 SQLi'), 4)
  assert.ok(parsed)
  assert.equal(parsed!.state, 'eval_end')
  assert.equal(parsed!.verdict, 'pass')
  assert.equal(parsed!.round, 2)
  assert.equal(parsed!.maxRounds, 3)
  assert.equal(parsed!.reason, '找到可复现 SQLi')
  assert.equal(parsed!.eventIndex, 4)
  assert.equal(evaluatorVerdictLabel(parsed!), '评估通过')
  assert.equal(evaluatorBadgeVariant(parsed!), 'success')
  assert.equal(evaluatorRoundText(parsed!), '第2/3轮')
})

test('fail and error are distinct; a start never claims a verdict', () => {
  const fail = parseEvaluatorEvent(status('eval_end', 'eval_end 第3/3轮：未通过 端口 443 未扫描'))!
  assert.equal(fail.verdict, 'fail')
  assert.equal(fail.reason, '端口 443 未扫描')
  assert.equal(evaluatorVerdictLabel(fail), '评估未通过')
  assert.equal(evaluatorBadgeVariant(fail), 'warning')
  assert.match(evaluatorSummaryText(fail), /端口 443/)

  const error = parseEvaluatorEvent(status('eval_error', 'eval_error 第2/3轮 provider timeout'))!
  assert.equal(error.verdict, 'error')
  assert.equal(error.reason, 'provider timeout')
  assert.equal(evaluatorVerdictLabel(error), '评估错误')
  assert.equal(evaluatorBadgeVariant(error), 'danger')

  const start = parseEvaluatorEvent(status('eval_start', 'eval_start 第1/3轮'))!
  assert.equal(start.verdict, 'start')
  assert.equal(start.reason, '')
  assert.equal(evaluatorVerdictLabel(start), '评估进行中')
  assert.equal(evaluatorRoundText(start), '第1/3轮')
})

test('the verdict is parsed from the status text too, and non-evaluator events are ignored', () => {
  // A host/older journal may only carry the folded text without event.state.
  const parsed = parseEvaluatorEvent({ kind: 'status', text: 'eval_end 第1/1轮：通过' })
  assert.equal(parsed?.verdict, 'pass')
  assert.equal(parseEvaluatorEvent({ kind: 'tool_call', text: 'eval_end is a word here' }), null)
  assert.equal(parseEvaluatorEvent({ kind: 'status', state: 'compaction', text: '已压缩' }), null)
  assert.deepEqual(parseEvaluatorResults([
    status('eval_start', 'eval_start 第1/2轮'),
    { kind: 'tool_call', at: '', text: '' },
    status('eval_end', 'eval_end 第1/2轮：未通过 扫描不完整'),
  ]).map((result) => result.verdict), ['start', 'fail'])
})

test('the latest judged round wins; a lone start stays "in progress"', () => {
  const results = parseEvaluatorResults([
    status('eval_start', 'eval_start 第1/2轮'),
    status('eval_end', 'eval_end 第1/2轮：未通过 继续'),
    status('eval_start', 'eval_start 第2/2轮'),
  ])
  const latest = latestEvaluatorResult(results)!
  assert.equal(latest.verdict, 'fail')
  assert.equal(latest.round, 1)
  const onlyStart = latestEvaluatorResult(parseEvaluatorResults([status('eval_start', 'eval_start 第1/3轮')]))!
  assert.equal(onlyStart.verdict, 'start')
  assert.equal(latestEvaluatorResult([]), null)
})

test('RenderBadge: the status strip shows pass/fail/error with the round', async () => {
  const server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  let renderer: ReactTestRenderer | undefined
  try {
    const { RunEvaluator } = await server.ssrLoadModule('/src/components/RunEvaluator.tsx')
    const events: Event[] = [
      status('eval_start', 'eval_start 第1/3轮'),
      status('eval_end', 'eval_end 第2/3轮：通过 找到可复现 SQLi'),
    ]
    await act(async () => {
      renderer = create(React.createElement(RunEvaluator, { events }))
    })
    const text = JSON.stringify(renderer!.toJSON())
    assert.ok(text.includes('评估通过'), text)
    assert.ok(text.includes('第2/3轮'), text)
    assert.ok(text.includes('找到可复现 SQLi'), text)
    assert.ok(text.includes('次评估'), text)
  } finally {
    await act(async () => { renderer?.unmount() })
    await server.close()
  }
})
