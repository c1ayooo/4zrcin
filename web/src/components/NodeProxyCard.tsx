import { useEffect, useRef, useState } from 'react'
import { Globe, Network, TriangleAlert } from 'lucide-react'
import { Badge, Button, Callout, Field, Input } from '@cyber/ui'
import { Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { settingsApplyNotice, settingsModelPayload } from '@/lib/settings'
import { useLoad, type AsyncState } from '@/lib/hooks'
import type { NodeProxyView, SettingsView } from '@/lib/types'

const NODE_PROXY_EXAMPLES = [
  { label: 'SOCKS5', value: 'socks5://127.0.0.1:1080' },
  { label: 'HTTP', value: 'http://127.0.0.1:8080' },
  { label: 'Trojan', value: 'trojan://user@relay.example:443' },
  { label: 'Clash 订阅', value: 'clash://https://sub.example/list' },
]

/** Client-side mirror of the server's URL checks: the same rules the console
 *  shows at the field instead of after a round trip. clash:// is a
 *  subscription spec, not a host/port endpoint, so it is exempt from the
 *  host rule. */
function nodeProxyProblem(url: string): string {
  const value = url.trim()
  if (value === '') return ''
  const schemeMatch = /^([a-zA-Z][a-zA-Z0-9+.-]*):\/\//.exec(value)
  if (!schemeMatch) return '代理地址必须以协议开头，如 socks5:// 或 clash://'
  const scheme = schemeMatch[1].toLowerCase()
  const allowed = ['http', 'https', 'socks5', 'trojan', 'vless', 'vmess', 'hysteria2', 'anytls', 'clash']
  if (!allowed.includes(scheme)) return `协议 ${scheme} 不在支持列表（${allowed.join('/')}）`
  if (scheme !== 'clash') {
    try {
      if (!new URL(value).host) return '代理地址缺少主机部分'
    } catch {
      return '代理地址不是可解析的 URL'
    }
  }
  return ''
}

/**
 * The node's scanner egress default — the operator's cyber.yaml top-level
 * `cyberhub.proxy`, the route every project on this node starts from. A
 * project-level proxy (project settings 出网代理) overrides it per session;
 * this card sets what applies when a project has none. MITM is executed by
 * the harness proxy; the console never dials anything itself. The value is
 * read by scan at process start, so the response's restart notice is part of
 * the contract, not an error.
 */
export function NodeProxyCard({ settings }: { settings: AsyncState<SettingsView> }) {
  return (
    <div className="space-y-4">
      {settings.error && <Callout tone="destructive">{settings.error}</Callout>}
      {settings.data?.node_proxy
        ? <ProxyEditor proxy={settings.data.node_proxy} settings={settings} />
        : !settings.loading && <Callout tone="info">当前实例没有代理配置投影（旧版服务端）。节点出网代理需在 cyber.yaml 中手工编辑 cyberhub.proxy。</Callout>}
    </div>
  )
}

function ProxyEditor({ proxy, settings }: { proxy: NodeProxyView; settings: AsyncState<SettingsView> }) {
  const [draft, setDraft] = useState(proxy.proxy || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [apply, setApply] = useState<{ tone: 'success' | 'warning' | 'info'; text: string } | null>(null)
  const baseline = useRef(proxy.proxy || '')
  const lock = useRef(false)
  useEffect(() => {
    const next = proxy.proxy || ''
    const old = baseline.current
    baseline.current = next
    setDraft(current => (current === old ? next : current))
  }, [proxy])
  const dirty = draft.trim() !== baseline.current
  const problem = nodeProxyProblem(draft)

  async function save() {
    if (lock.current || !settings.data || !dirty) return
    if (problem) { setError(problem); return }
    lock.current = true; setBusy(true); setError(''); setApply(null)
    try {
      // A PUT applies the whole settings view, so the model half rides along
      // (same discipline as the capture card); the egress proxy is the one
      // added field. It never echoes back — the refreshed projection decides.
      const next = await api.saveSettings({
        ...settingsModelPayload(settings.data),
        node_proxy_write: { proxy: draft.trim() },
      })
      settings.setData(next)
      baseline.current = next.node_proxy?.proxy || ''
      setDraft(baseline.current)
      setApply(settingsApplyNotice(next))
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) } finally { lock.current = false; setBusy(false) }
  }

  return (
    <Panel
      title="出网代理（节点默认）"
      extra={<Badge variant="outline" size="sm">cyber.yaml cyberhub.proxy · scan 启动时读取</Badge>}
      bodyClassName="space-y-3"
      data-testid="node-proxy"
    >
      <div className="flex flex-wrap items-center gap-2">
        {proxy.configured ? (
          <Network className="h-4 w-4 text-success" aria-hidden="true" />
        ) : (
          <Globe className="h-4 w-4 text-ink-3" aria-hidden="true" />
        )}
        <Badge size="sm" variant={proxy.configured ? 'success' : 'muted'}>
          {proxy.configured ? '已配置' : '直连（未配置）'}
        </Badge>
        {proxy.env_override && <Badge size="sm" variant="warning">CYBER_PROXY 环境变量覆盖中</Badge>}
      </div>

      {proxy.reason && (
        <Callout tone="warning" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
          {proxy.reason}
        </Callout>
      )}

      <p className="text-[12px] leading-relaxed text-ink-2">
        本节点的扫描工具流量默认经此代理转发；某个项目在项目设置里配置了自己的出网代理时，
        该项目以自己的为准，其余项目继续走这里。记录与拦截（MITM）发生在执行节点的代理内部，与上游选择无关。
      </p>

      <Field
        label="代理地址"
        hint="留空 = 直连。支持 socks5/http/trojan/vless/vmess/hysteria2/anytls，clash:// 为订阅地址（自动负载均衡）。"
      >
        <Input
          value={draft}
          onChange={e => { setDraft(e.target.value); setError('') }}
          disabled={busy}
          aria-label="节点出网代理地址"
          spellCheck={false}
          autoCapitalize="none"
          placeholder="socks5://127.0.0.1:1080"
          className="font-mono text-[12px]"
        />
      </Field>
      <div className="flex flex-wrap gap-1.5">
        {NODE_PROXY_EXAMPLES.map(ex => (
          <Button
            key={ex.label}
            type="button"
            size="xs"
            variant="outline"
            disabled={busy}
            onClick={() => { setDraft(ex.value); setError('') }}
          >
            {ex.label}
          </Button>
        ))}
      </div>

      <dl className="grid grid-cols-1 gap-x-4 gap-y-1.5 text-[12px] leading-relaxed">
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">当前保存</dt>
          <dd className="tw-break min-w-0 font-mono text-[11px] text-ink-2">
            {proxy.configured ? proxy.proxy : '未配置（直连）'}
          </dd>
        </div>
        {proxy.source && (
          <div className="flex min-w-0 items-baseline gap-1.5">
            <dt className="shrink-0 text-ink-3">生效来源</dt>
            <dd className="tw-break min-w-0 text-[11px] text-ink-2">{proxy.source}</dd>
          </div>
        )}
      </dl>

      {error && <p className="text-[12px] text-destructive" role="alert">{error}</p>}
      {apply && <Callout tone={apply.tone}>{apply.text}</Callout>}

      <div className="flex flex-wrap items-center gap-2 border-t border-hairline pt-2.5">
        <Button
          type="button"
          size="sm"
          disabled={!dirty || busy || !!problem}
          data-testid="node-proxy-save"
          onClick={() => { void save() }}
        >
          保存出网代理
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!dirty || busy}
          onClick={() => { setDraft(baseline.current); setError('') }}
        >
          重置
        </Button>
        <span className="text-[11px] text-ink-3">
          保存后写入 cyber.yaml；scan 在启动时读取该值，需重启本机宿主后生效。
        </span>
      </div>
    </Panel>
  )
}
