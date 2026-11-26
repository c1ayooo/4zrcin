import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  commandProblem,
  domainProblem,
  emptyGlobalFence,
  fenceListArmed,
  fenceSummary,
  forSave,
  hitActionLabel,
  hitCounts,
  hitKindLabel,
  hitRuleText,
  hitSourceLabel,
  needsEnableConfirm,
  normalizeClientFence,
  withCommandAdded,
  withCommandRemoved,
  withCommandUpdated,
  withDomainAdded,
  withDomainRemoved,
  withDomainUpdated,
} from '../src/lib/global-fence.ts'
import type { FenceHit } from '../src/lib/types.ts'

const base = {
  ...emptyGlobalFence(),
  commands: [{ id: 'gf-1', pattern: 'curl *', desc: '禁止下载', action: 'deny' as const }],
  domains: [{ id: 'gf-2', host: 'evil.example' }],
}

// The list transformations keep identity stable: an edit never rebinds the
// row's id (the audit names rows by id alone).
test('list edits keep the row identity', () => {
  const added = withCommandAdded(base, { pattern: 'rm -rf', desc: '', action: 'review' })
  assert.equal(added.commands.length, 2)
  assert.ok(added.commands[1].id.startsWith('draft-'))
  const updated = withCommandUpdated(added, added.commands[1].id, { action: 'deny', desc: '危险' })
  assert.equal(updated.commands[1].id, added.commands[1].id)
  assert.equal(updated.commands[1].action, 'deny')
  const removed = withCommandRemoved(updated, added.commands[1].id)
  assert.deepEqual(removed.commands.map((r) => r.id), ['gf-1'])

  const domain = withDomainAdded(base, { id: '', host: 'gov.cn' })
  assert.equal(domain.domains.length, 2)
  const host = domain.domains[1].id
  assert.equal(withDomainUpdated(domain, host, { host: 'gov.cn ' }).domains[1].host, 'gov.cn ')
  assert.deepEqual(withDomainRemoved(domain, host).domains.map((r) => r.id), ['gf-2'])
})

// The wire omits empty arrays and false switches; the page always gets both.
test('an empty wire fence becomes a full client shape', () => {
  assert.deepEqual(normalizeClientFence({}), emptyGlobalFence())
  assert.deepEqual(normalizeClientFence(undefined), emptyGlobalFence())
  const partial = normalizeClientFence({ enabled_commands: true, commands: [{ id: 'gf-1', pattern: 'x', action: 'deny' }] })
  assert.deepEqual(partial.domains, [])
  assert.equal(partial.enabled_commands, true)
})

// Placeholder ids are a client detail; the server owns the stable id.
test('saving drops placeholder ids and keeps real ones', () => {
  const fence = withCommandAdded(withDomainAdded(base, { id: '', host: 'gov.cn' }), {
    pattern: 'rm -rf', desc: '', action: 'deny',
  })
  const wire = forSave(fence)
  assert.deepEqual(wire.commands.map((r) => r.id), ['gf-1', ''])
  assert.deepEqual(wire.domains.map((r) => r.id), ['gf-2', ''])
})

test('a list is armed only when enabled and non-empty', () => {
  assert.equal(fenceListArmed(true, base.commands), true)
  assert.equal(fenceListArmed(false, base.commands), false)
  assert.equal(fenceListArmed(true, []), false)
  const summary = fenceSummary(base)
  assert.equal(summary.commands, 1)
  assert.equal(summary.denyCommands, 1)
  assert.equal(summary.commandsArmed, false)
  const armed = fenceSummary({ ...base, enabled_commands: true, enabled_domains: true })
  assert.equal(armed.commandsArmed, true)
  assert.equal(armed.domainsArmed, true)
})

// Enabling rows is the dangerous direction; disabling never is.
test('enabling a non-empty list needs confirmation', () => {
  assert.equal(needsEnableConfirm(base.commands, true), true)
  assert.equal(needsEnableConfirm([], true), false)
  assert.equal(needsEnableConfirm(base.commands, false), false)
})

// Client pre-checks mirror the server's bounds, not its grammar (Go RE2 stays
// the authority).
test('client validation stops obviously broken rows', () => {
  assert.match(commandProblem('', '') ?? '', /匹配模式/)
  assert.match(commandProblem('x'.repeat(201), '') ?? '', /最多/)
  assert.match(commandProblem('curl *', 'y'.repeat(201)) ?? '', /说明/)
  assert.equal(commandProblem('curl *', ''), undefined)
  assert.match(domainProblem('') ?? '', /域名/)
  assert.match(domainProblem('https://evil.example') ?? '', /协议/)
  assert.match(domainProblem('evil.example:443') ?? '', /协议|端口|路径/)
  assert.equal(domainProblem('10.0.0.0/8'), undefined)
  assert.equal(domainProblem('gov.cn'), undefined)
})

function hit(over: Partial<FenceHit>): FenceHit {
  return {
    at: '2026-03-01T10:00:00Z',
    project_id: 'p1',
    project: '测试',
    source: 'global',
    kind: 'domain',
    rule_id: 'gf-1',
    content: 'https://evil.example/x',
    action: 'block',
    ...over,
  }
}

test('audit labels never invent a state', () => {
  assert.equal(hitSourceLabel('global'), '全局')
  assert.equal(hitSourceLabel('rule'), '项目规则')
  assert.equal(hitSourceLabel('other'), '未记录')
  assert.equal(hitKindLabel('command'), '命令')
  assert.equal(hitActionLabel({ action: 'block' }), '拒绝')
  assert.equal(hitActionLabel({ action: 'review', state: 'pending' }), '待审')
  assert.equal(hitActionLabel({ action: 'review', state: 'approved' }), '已批准')
  assert.equal(hitActionLabel({ action: 'review', state: 'rejected' }), '已拒绝')
  assert.equal(hitRuleText({ rule_text: 'review * out.example /', rule_id: 'sr-2' }), 'review * out.example /')
  assert.equal(hitRuleText({ rule_id: 'gf-9' }), 'gf-9')
})

test('hit counts split by source and action', () => {
  const counts = hitCounts([
    hit({}),
    hit({ source: 'rule', action: 'review' }),
    hit({ kind: 'command', action: 'review' }),
  ])
  assert.deepEqual(counts, { global: 2, rule: 1, block: 1, review: 2 })
})
