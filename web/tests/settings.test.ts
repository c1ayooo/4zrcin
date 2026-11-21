import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  checkProbeSummary,
  mcpServersDraftText,
  mergeProjectSettings,
  nonNegativeInteger,
  parseMCPServersDraft,
  projectSettingsDraft,
  projectSettingsPatch,
  retentionBytesFromDraft,
  retentionDraft,
  retentionHuman,
  retentionMibFromBytes,
  retentionProblem,
  retentionStatusText,
  searchKeyStatus,
  settingsApplyNotice,
  settingsModelPayload,
} from '../src/lib/settings.ts'
import type { MCPServerConfig, ProjectInfo, SettingsView } from '../src/lib/types.ts'

const project = { id: 'p', max_runs: null, round_timeout: '' } as ProjectInfo

test('refresh preserves unsaved settings while updating untouched settings', () => {
  const previous = projectSettingsDraft(project)
  const incoming = projectSettingsDraft({ ...project, max_runs: 8 })
  const draft = { ...previous, max_runs: '3' }
  // The field the operator is editing keeps the typed value; nothing else drifts.
  assert.deepEqual(mergeProjectSettings(previous, incoming, draft), { ...draft })
})

test('project save distinguishes inheritance from zero and only updates changed fields', () => {
  const previous = projectSettingsDraft({ ...project, max_runs: 4 })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, max_runs: '' }), { max_runs: null })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, max_runs: '0' }), { max_runs: 0 })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, round_timeout: '15m' }), { round_timeout: '15m' })
  // The brief and the constraint document are documents now, not settings: the
  // project endpoint rejects them (400 document_write_moved), so no draft field
  // may carry them back out through this patch.
  assert.deepEqual(Object.keys(previous), ['max_runs', 'round_timeout'])
  const smuggled = { ...previous, scope: 'changed', description: 'changed' } as unknown as typeof previous
  assert.deepEqual(projectSettingsPatch(previous, smuggled), {})
  // The retired review switch never rides along, even when a stale caller puts
  // it in the draft: the server rejects that field outright.
  const stale = { ...previous, verify_auto: 'on' } as unknown as typeof previous
  assert.deepEqual(projectSettingsPatch(previous, stale), {})
})

test('invalid numeric input cannot silently turn into an unlimited value', () => {
  for (const raw of ['-1', '1.5', '1e3', 'NaN', '9007199254740992']) assert.throws(() => nonNegativeInteger(raw, '并发'))
  assert.equal(nonNegativeInteger('', '并发'), 0)
  assert.equal(nonNegativeInteger(' 12 ', '并发'), 12)
})

test('MCP JSON draft keeps masked secrets and distinguishes keep from clear', () => {
  // The read path masks env/header values; a round-trip must send the
  // placeholder back (the server merges the stored value) instead of a guess.
  const stored: Record<string, MCPServerConfig> = {
    playwright: { command: 'npx', args: ['-y', '@playwright/mcp'], env: { TOKEN: '***' }, tools: null },
  }
  const text = mcpServersDraftText(stored)
  assert.match(text, /"TOKEN": "\*\*\*"/)
  const parsed = parseMCPServersDraft(text)
  assert.equal(parsed.error, undefined)
  assert.deepEqual(parsed.value, stored)
  // Empty text and {} are the explicit "remove every server" value.
  assert.deepEqual(parseMCPServersDraft(''), { value: {}, warnings: [] })
  assert.deepEqual(parseMCPServersDraft('  {}  '), { value: {}, warnings: [] })
  assert.equal(mcpServersDraftText(undefined), '')
})

test('MCP JSON draft rejects the same configuration mistakes the host would', () => {
  assert.match(parseMCPServersDraft('{oops').error ?? '', /不是合法 JSON/)
  assert.match(parseMCPServersDraft('[]').error ?? '', /必须是 JSON 对象/)
  assert.match(parseMCPServersDraft('{"bad alias": {"command":"x"}}').error ?? '', /别名/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","url":"https://e"}}').error ?? '', /必须且只能配置/)
  assert.match(parseMCPServersDraft('{"a":{}}').error ?? '', /必须且只能配置/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","headers":{"K":"v"}}}').error ?? '', /headers 只能与 url/)
  assert.match(parseMCPServersDraft('{"a":{"url":"https://e","env":{"K":"v"}}}').error ?? '', /args\/env\/cwd 只能与 command/)
  assert.match(parseMCPServersDraft('{"a":{"url":"ftp://e"}}').error ?? '', /HTTP\(S\)/)
  assert.match(parseMCPServersDraft('{"a":{"url":"https://u:p@e"}}').error ?? '', /用户名/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","args":"-y"}}').error ?? '', /args 必须是字符串数组/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","env":{"K":1}}}').error ?? '', /必须是字符串/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","tools":["a","a"]}}').error ?? '', /重复/)
  assert.match(parseMCPServersDraft('{"a":{"command":"x","timeoutSeconds":-1}}').error ?? '', /非负整数/)
  // Unknown fields are dropped with a warning instead of silently disappearing.
  const unknown = parseMCPServersDraft('{"a":{"command":"x","frobnicate":true}}')
  assert.equal(unknown.error, undefined)
  assert.equal(unknown.value?.a.command, 'x')
  assert.equal(unknown.warnings.length, 1)
  assert.match(unknown.warnings[0], /frobnicate/)
  // A URL server may carry headers; a command server may carry env.
  const valid = parseMCPServersDraft('{"web":{"url":"https://mcp.example/sse","headers":{"Authorization":"***"}}}')
  assert.equal(valid.error, undefined)
  assert.deepEqual(valid.value, { web: { url: 'https://mcp.example/sse', headers: { Authorization: '***' }, tools: null } })
})

test('save outcome distinguishes online apply, restart and plain save', () => {
  assert.equal(settingsApplyNotice({ applied: 'online' })?.tone, 'success')
  assert.match(settingsApplyNotice({ applied: 'online' })?.text ?? '', /已在线应用/)
  const restart = settingsApplyNotice({ applied: 'restart', requires_restart: true, restart_reason: 'MCP/search 只在 scan 启动时读取' })
  assert.equal(restart?.tone, 'warning')
  assert.match(restart?.text ?? '', /重启/)
  assert.match(restart?.text ?? '', /MCP\/search/)
  const saved = settingsApplyNotice({ applied: 'saved', restart_reason: '本机没有运行中的宿主' })
  assert.equal(saved?.tone, 'info')
  assert.match(saved?.text ?? '', /本机没有运行中的宿主/)
  // A product-only PUT must carry the model half or the server would clear it.
  assert.deepEqual(
    settingsModelPayload({ base_url: 'http://gw/v1', model: 'm', provider: 'openai', context_window: 0, max_tokens: 4096 }),
    { base_url: 'http://gw/v1', model: 'm', provider: 'openai', context_window: 0, max_tokens: 4096 },
  )
})

test('connection probe and search-key status say where the answer came from', () => {
  const host = checkProbeSummary({ available: true, source: 'host', returned_model: 'm-1', ms: 42 })
  assert.equal(host.tone, 'success')
  assert.match(host.text, /运行中的宿主实测/)
  assert.match(host.text, /m-1/)
  const self = checkProbeSummary({ available: true, source: 'self' })
  assert.match(self.text, /控制器自检/)
  const down = checkProbeSummary({ available: false, source: 'self', error: '连不上网关' })
  assert.equal(down.tone, 'warning')
  assert.match(down.text, /连不上网关/)
  assert.equal(searchKeyStatus({ search_has_key: false }), '未配置 Tavily key（联网搜索不可用）')
  assert.match(searchKeyStatus({ search_has_key: true, search_key_count: 2 }), /已保存 2 个/)
  assert.ok(!searchKeyStatus({ search_has_key: true, search_key_count: 1 }).includes('tvly'))
})

test('product save keeps the model half of the settings view intact', () => {
  const view = { base_url: 'http://gw/v1', model: 'm', provider: 'anthropic', context_window: 8000, max_tokens: 1000, api_key_set: true, configured: true, path: 'x' } as SettingsView
  assert.deepEqual(settingsModelPayload(view), {
    base_url: 'http://gw/v1', model: 'm', provider: 'anthropic', context_window: 8000, max_tokens: 1000,
  })
})

test('traffic retention edits MiB but defaults to 2 GiB and never shows 0', () => {
  const gib = 1024 * 1024 * 1024
  assert.equal(retentionDraft(undefined), '')
  // 0 is the wire's "no explicit value", not a measured 0 MiB.
  assert.equal(retentionDraft(0), '')
  assert.equal(retentionDraft(2 * gib), '2048')
  assert.equal(retentionDraft(4 * gib), '4096')
  assert.equal(retentionMibFromBytes(16 * 1024 * 1024), 16)
  assert.equal(retentionMibFromBytes(0), 0)
  assert.equal(retentionStatusText(undefined), '默认 2 GiB（未显式设置）')
  assert.equal(retentionStatusText(0), '默认 2 GiB（未显式设置）')
  assert.equal(retentionStatusText(4 * gib), '已设置 4,096 MiB（4 GiB）')
  assert.equal(retentionHuman(gib), '1 GiB')
  assert.equal(retentionHuman(512 * 1024 * 1024), '512 MiB')
})

test('traffic retention validates MiB integers within 16 MiB..1 TiB locally', () => {
  assert.equal(retentionProblem(''), null)
  assert.equal(retentionProblem(' 16 '), null)
  assert.equal(retentionProblem('2048'), null)
  assert.equal(retentionProblem('1048576'), null)
  assert.match(retentionProblem('8') ?? '', /最小 16 MiB/)
  assert.match(retentionProblem('0') ?? '', /最小 16 MiB/)
  assert.match(retentionProblem('-1') ?? '', /MiB 整数/)
  assert.match(retentionProblem('1.5') ?? '', /MiB 整数/)
  assert.match(retentionProblem('1e3') ?? '', /MiB 整数/)
  assert.match(retentionProblem('1048577') ?? '', /1,048,576 MiB（1 TiB）/)
  assert.equal(retentionBytesFromDraft('2048'), 2048 * 1024 * 1024)
  // Empty sends 0: the backend fills the generated 2 GiB default.
  assert.equal(retentionBytesFromDraft(''), 0)
})
