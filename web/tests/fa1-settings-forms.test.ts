// The settings forms validate before the request so the error can name the
// field and route the focus. These are the pure rules the panel uses.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { concurrencyProblem, connectionProblems } from '../src/lib/settings.ts'

test('blank connection numbers mean "leave it to the gateway" and are accepted', () => {
  assert.deepEqual(connectionProblems({ context_window: '', max_tokens: '  ' }), [])
})

test('typed connection numbers must be non-negative safe integers', () => {
  assert.deepEqual(connectionProblems({ context_window: '128000', max_tokens: '4096' }), [])
  for (const raw of ['-1', '1.5', '1e3', 'NaN', '9007199254740992', '12ms']) {
    assert.equal(connectionProblems({ context_window: raw, max_tokens: '' }).length, 1, raw)
  }
  const both = connectionProblems({ context_window: 'x', max_tokens: 'y' })
  assert.deepEqual(both.map((problem) => problem.field), ['context_window', 'max_tokens'])
  assert.match(both[0].message, /上下文窗口/)
  assert.match(both[1].message, /输出上限/)
})

test('runtime concurrency accepts only 1..max integers', () => {
  assert.equal(concurrencyProblem('1', 8), null)
  assert.equal(concurrencyProblem('8', 8), null)
  for (const raw of ['', '0', '9', '2.5', '-1', 'many']) {
    assert.match(concurrencyProblem(raw, 8) ?? '', /1–8/)
  }
  assert.equal(concurrencyProblem('4', 8), null)
})
