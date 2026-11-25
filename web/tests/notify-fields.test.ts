import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  CHANNEL_FIELDS,
  SEVERITY_OPTIONS,
  formatHeaderLines,
  formatRecipients,
  parseHeaderLines,
  parseRecipients,
  type ChannelFieldDef,
} from '../src/lib/notify-fields.ts'

function buildConfig(kind: string, values: Record<string, string>): Record<string, unknown> {
  const defs: ChannelFieldDef[] = CHANNEL_FIELDS[kind] ?? []
  const out: Record<string, unknown> = {}
  for (const def of defs) {
    const v = values[def.key]
    if (def.key === 'to') {
      const list = parseRecipients(v ?? '')
      if (list.length > 0) out.to = list
      continue
    }
    if (def.key === 'headers') {
      const headers = parseHeaderLines(v ?? '')
      if (Object.keys(headers).length > 0) out.headers = headers
      continue
    }
    if (v === undefined || v === '') continue
    if (def.key === 'tls') {
      out.tls = v === 'true'
      continue
    }
    if (def.type === 'number') {
      const n = Number(v)
      if (Number.isFinite(n) && n > 0) out[def.key] = n
      continue
    }
    out[def.key] = v
  }
  return out
}

test('parseHeaderLines 解析多行 Key: Value，忽略空行与缺冒号的行', () => {
  assert.deepEqual(parseHeaderLines('Authorization: Bearer t\nX-Token: s\n\n没有冒号\n: 空'), {
    Authorization: 'Bearer t',
    'X-Token': 's',
  })
})

test('formatHeaderLines/parseHeaderLines 往返一致', () => {
  const headers = { Authorization: 'Bearer t', 'X-Token': 's' }
  assert.deepEqual(parseHeaderLines(formatHeaderLines(headers)), headers)
})

test('parseRecipients 兼容逗号/中文逗号/分号/换行分隔并去空白', () => {
  assert.deepEqual(parseRecipients('a@x.com, b@x.com\nc@x.com；d@x.com； e@x.com'), [
    'a@x.com', 'b@x.com', 'c@x.com', 'd@x.com', 'e@x.com',
  ])
})

test('formatRecipients 把数组渲染回文本框内容', () => {
  assert.equal(formatRecipients(['a@x.com', 'b@x.com']), 'a@x.com, b@x.com')
  assert.equal(formatRecipients(undefined), '')
})

test('buildConfig 按 dingtalk 字段表产出 webhook/secret', () => {
  const cfg = buildConfig('dingtalk', { webhook: 'https://oapi…', secret: 'SEC1' })
  assert.deepEqual(cfg, { webhook: 'https://oapi…', secret: 'SEC1' })
})

test('buildConfig 把 email 的收件人转数组、端口转数字、TLS 转布尔', () => {
  const cfg = buildConfig('email', {
    host: 'smtp.example.com', port: '465', tls: 'true',
    from: 'noreply@x.com', to: 'a@x.com, b@x.com',
  })
  assert.deepEqual(cfg, {
    host: 'smtp.example.com',
    port: 465,
    tls: true,
    from: 'noreply@x.com',
    to: ['a@x.com', 'b@x.com'],
  })
})

test('buildConfig 的 webhook 自定义头转映射，空值字段不落库', () => {
  const cfg = buildConfig('webhook', {
    url: 'https://x/hook', method: 'POST',
    headers: 'X-Token: s', body_template: '', content_type: '',
  })
  assert.deepEqual(cfg, { url: 'https://x/hook', method: 'POST', headers: { 'X-Token': 's' } })
})

test('每种通道类型都有字段表，secret 类字段用 password 控件', () => {
  for (const kind of ['dingtalk', 'feishu', 'wecom', 'webhook', 'telegram', 'email']) {
    assert.ok(CHANNEL_FIELDS[kind]?.length, `${kind} 缺字段表`)
  }
  assert.equal(CHANNEL_FIELDS.dingtalk.find((f) => f.key === 'secret')?.type, 'password')
  assert.equal(CHANNEL_FIELDS.email.find((f) => f.key === 'password')?.type, 'password')
})

test('级别门槛选项覆盖完整四级并可回到不限', () => {
  assert.deepEqual(SEVERITY_OPTIONS.map((o) => o.value), ['', 'low', 'medium', 'high', 'critical'])
})
