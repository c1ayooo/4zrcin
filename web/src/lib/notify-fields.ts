// 通知通道的字段表（docs/05-im-notify.md）。
//
// 表驱动与 4zreco 的 channel-fields.ts 同思路：新增一种通道类型（或调整某
// 通道的连接参数）只需要改这个数据文件，页面与请求层不动。secret_keys /
// destination_keys 由服务端下发，这里只描述「表单怎么渲染」。

export interface ChannelFieldDef {
  key: string
  label: string
  type: 'text' | 'password' | 'number' | 'textarea' | 'select'
  placeholder?: string
  hint?: string
  options?: { value: string; label: string }[]
}

export const CHANNEL_FIELDS: Record<string, ChannelFieldDef[]> = {
  dingtalk: [
    { key: 'webhook', label: 'Webhook 地址', type: 'text', placeholder: 'https://oapi.dingtalk.com/robot/send?access_token=…', hint: '钉钉自定义机器人的 Webhook 地址（本身即凭据，回显掩码）' },
    { key: 'secret', label: '加签密钥', type: 'password', placeholder: 'SEC…（留空保持不变）', hint: '机器人安全设置选择「加签」时填写' },
  ],
  feishu: [
    { key: 'webhook', label: 'Webhook 地址', type: 'text', placeholder: 'https://open.feishu.cn/open-apis/bot/v2/hook/…' },
    { key: 'secret', label: '签名校验密钥', type: 'password', placeholder: '留空表示未启用签名校验' },
  ],
  wecom: [
    { key: 'webhook', label: 'Webhook 地址', type: 'text', placeholder: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…' },
  ],
  webhook: [
    { key: 'url', label: '目标 URL', type: 'text', placeholder: 'https://…' },
    {
      key: 'method', label: 'HTTP 方法', type: 'select',
      options: [
        { value: 'POST', label: 'POST（默认）' },
        { value: 'GET', label: 'GET' },
        { value: 'PUT', label: 'PUT' },
        { value: 'PATCH', label: 'PATCH' },
      ],
    },
    { key: 'content_type', label: 'Content-Type', type: 'text', placeholder: 'application/json' },
    { key: 'headers', label: '自定义请求头', type: 'textarea', placeholder: 'Authorization: Bearer …\nX-Token: …', hint: '每行一条「Key: Value」' },
    { key: 'body_template', label: '请求体模板', type: 'textarea', placeholder: '{"title": {{json .Title}}, …}', hint: 'Go template；留空用默认模板。{{json .X}} 会做 JSON 转义' },
  ],
  telegram: [
    { key: 'bot_token', label: 'Bot Token', type: 'password', placeholder: '123456:ABC-DEF…' },
    { key: 'chat_id', label: 'Chat ID', type: 'text', placeholder: '群组 -100… 或个人数字 id' },
    { key: 'base_url', label: 'API 地址', type: 'text', placeholder: '默认 https://api.telegram.org（可填代理）' },
  ],
  email: [
    { key: 'host', label: 'SMTP 主机', type: 'text', placeholder: 'smtp.example.com' },
    { key: 'port', label: '端口', type: 'number', placeholder: '465 / 587' },
    {
      key: 'tls', label: 'TLS', type: 'select',
      options: [
        { value: 'false', label: 'STARTTLS（587 常用）' },
        { value: 'true', label: '隐式 TLS（465 常用）' },
      ],
    },
    { key: 'username', label: '用户名', type: 'text' },
    { key: 'password', label: '密码 / 授权码', type: 'password' },
    { key: 'from', label: '发件人', type: 'text', placeholder: 'noreply@example.com' },
    { key: 'to', label: '收件人', type: 'textarea', placeholder: 'a@example.com, b@example.com', hint: '多个收件人用逗号或换行分隔' },
  ],
}

/** 级别门槛选项；空=不限。门槛只作用于携带级别的漏洞事件。 */
export const SEVERITY_OPTIONS = [
  { value: '', label: '不限' },
  { value: 'low', label: '低危及以上' },
  { value: 'medium', label: '中危及以上' },
  { value: 'high', label: '高危及以上' },
  { value: 'critical', label: '仅严重' },
]

/** 把多行文本框内容解析成 webhook 的自定义头。 */
export function parseHeaderLines(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of (raw ?? '').split(/\r?\n/)) {
    const i = line.indexOf(':')
    if (i <= 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k) out[k] = v
  }
  return out
}

/** 把自定义头渲染回多行文本。 */
export function formatHeaderLines(headers: Record<string, unknown> | undefined): string {
  if (!headers || typeof headers !== 'object') return ''
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${String(v ?? '')}`)
    .join('\n')
}

/** 把收件人文本解析成后端要求的字符串数组。 */
export function parseRecipients(raw: string): string[] {
  return (raw ?? '')
    .split(/[\r\n,，;；]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** 把收件人数组渲染回文本框内容。 */
export function formatRecipients(v: unknown): string {
  if (Array.isArray(v)) return v.join(', ')
  return typeof v === 'string' ? v : ''
}
