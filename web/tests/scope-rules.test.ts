import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  SCOPE_RULE_ACTIONS,
  scopeHostProblem,
  scopeMethodProblem,
  scopePathProblem,
  scopeRulesDraftFromProject,
  scopeRulesPayload,
  scopeRulesProblems,
  scopeRulesSummary,
  type ScopeRulesDraft,
} from '../src/lib/settings.ts'

function validDraft(): ScopeRulesDraft {
  return {
    enabled: true,
    shadow: false,
    default_action: 'review',
    rules: [
      { action: 'record', method: '*', host: '*.target-corp.example', path_prefix: '/' },
      { action: 'review', method: 'post|delete', host: '*', path_prefix: '/api/' },
    ],
  }
}

// ---- row cells ---------------------------------------------------------------

test('host cells accept the intended shapes and reject everything else', () => {
  assert.equal(scopeHostProblem('*.target-corp.example'), null)
  assert.equal(scopeHostProblem('Target-Corp.Example'), null)
  assert.equal(scopeHostProblem('.target-corp.example'), null)
  assert.equal(scopeHostProblem('*'), null)
  assert.match(scopeHostProblem('https://a.example.com') ?? '', /协议/)
  assert.match(scopeHostProblem('a.example.com:8080') ?? '', /端口/)
  assert.match(scopeHostProblem('a.example.com/api') ?? '', /路径/)
  assert.match(scopeHostProblem('a..example.com') ?? '', /主机名/)
  assert.match(scopeHostProblem('a.*.example.com') ?? '', /通配/)
  assert.match(scopeHostProblem('') ?? '', /必填/)
})

test('method cells take * or verb lists, dedup checked', () => {
  assert.equal(scopeMethodProblem('*'), null)
  assert.equal(scopeMethodProblem('get|POST'), null)
  assert.match(scopeMethodProblem('BREW') ?? '', /未知方法/)
  assert.match(scopeMethodProblem('POST||GET') ?? '', /空的方法项/)
  assert.match(scopeMethodProblem('POST|POST') ?? '', /重复/)
  assert.match(scopeMethodProblem('') ?? '', /必填/)
})

test('path prefix: empty means /, otherwise must start with a slash', () => {
  assert.equal(scopePathProblem(''), null)
  assert.equal(scopePathProblem('/api/v2/'), null)
  assert.match(scopePathProblem('api/') ?? '', /以 \/ 开头/)
  assert.match(scopePathProblem('/a b') ?? '', /空白/)
})

// ---- whole-draft validation ----------------------------------------------------

test('a valid draft reports no problems', () => {
  assert.equal(scopeRulesProblems(validDraft()), null)
})

test('problems land on their own rows, set-level problems on the header', () => {
  const draft = validDraft()
  draft.rules[1].host = 'a.example.com:8080'
  const problems = scopeRulesProblems(draft)
  assert.equal(problems?.rows[0], null)
  assert.match(problems?.rows[1] ?? '', /端口/)
})

test('duplicate rows are rejected even when every cell is valid', () => {
  const draft = validDraft()
  draft.rules[1] = { ...draft.rules[0] }
  const problems = scopeRulesProblems(draft)
  assert.match(problems?.default_action ?? '', /重复规则/)
})

test('rules validate even while the switch is off', () => {
  // A disabled draft that only validates when enabled would arm rules the
  // engine rejects the moment the operator flips the switch.
  const draft = validDraft()
  draft.enabled = false
  draft.default_action = 'nope' as ScopeRulesDraft['default_action']
  const problems = scopeRulesProblems(draft)
  assert.match(problems?.default_action ?? '', /default_action/)
})

// ---- draft/payload -------------------------------------------------------------

test('absent stored rules become a disabled empty draft, not a synthesized default', () => {
  const draft = scopeRulesDraftFromProject({ scope_rules: null })
  assert.deepEqual(draft, { enabled: false, shadow: false, default_action: 'review', rules: [] })
  assert.equal(scopeRulesSummary(null), '未配置（仅记录）')
})

test('the payload is normalized the way the server stores it', () => {
  const draft = validDraft()
  draft.rules[1].method = ' post|delete '
  draft.rules[1].host = ' .Fallback.Example '
  draft.rules[1].path_prefix = ''
  const payload = scopeRulesPayload(draft)
  assert.equal(payload.enabled, true)
  assert.equal(payload.default_action, 'review')
  assert.equal(payload.rules[1].method, 'POST|DELETE')
  assert.equal(payload.rules[1].host, '*.fallback.example')
  assert.equal(payload.rules[1].path_prefix, '/')
  assert.match(payload.rules[0].id, /^sr-\d+$/)
})

test('summary states the three honest states: unconfigured, draft, enabled', () => {
  assert.match(scopeRulesSummary({ enabled: false, default_action: 'review', rules: [] }), /草稿/)
  assert.match(scopeRulesSummary({ enabled: true, default_action: 'review', rules: [] }), /已启用/)
  assert.match(scopeRulesSummary({ enabled: true, default_action: 'block', shadow: true, rules: [] }), /影子模式/)
  assert.deepEqual(SCOPE_RULE_ACTIONS, ['record', 'review', 'block'])
})
