import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Callout, Field, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Spinner, Switch } from '@cyber/ui'
import { Panel } from './Page'
import { api } from '@/lib/api'
import { EGRESS_PROXY_SCHEMES, type EgressProxyScheme, type ProjectInfo } from '@/lib/types'

const SCHEME_LABELS: Record<EgressProxyScheme, string> = {
  http: 'HTTP',
  https: 'HTTPS',
  socks5: 'SOCKS5',
  trojan: 'Trojan',
  vless: 'VLESS',
  vmess: 'VMess',
  hysteria2: 'Hysteria2',
  anytls: 'AnyTLS',
}

const DEFAULT_PORT: Record<EgressProxyScheme, string> = {
  http: '80', https: '443', socks5: '1080',
  trojan: '', vless: '', vmess: '', hysteria2: '', anytls: '',
}

interface ProxyDraft {
  enabled: boolean
  scheme: EgressProxyScheme
  host: string
  port: string
  username: string
  password: string
}

function draftFromProject(project: ProjectInfo): ProxyDraft {
  const stored = project.proxy
  return {
    enabled: stored?.enabled ?? false,
    scheme: stored?.scheme ?? 'socks5',
    host: stored?.host ?? '',
    port: stored?.port ? String(stored.port) : '',
    username: stored?.username ?? '',
    password: stored?.password ?? '',
  }
}

function payload(draft: ProxyDraft) {
  return {
    enabled: draft.enabled,
    scheme: draft.scheme,
    host: draft.host.trim(),
    port: draft.port.trim() === '' ? undefined : Number(draft.port.trim()),
    username: draft.username === '' ? undefined : draft.username,
    password: draft.password === '' ? undefined : draft.password,
  }
}

/** Client-side mirror of the server's NormalizeEgressProxy: the same rules, so
 *  the operator sees the problem at the field instead of after a round trip. */
function problems(draft: ProxyDraft): string | null {
  if (draft.host.trim() === '') return '代理主机不能为空'
  if (/[\s/@]/.test(draft.host.trim())) return '代理主机只能是主机名或 IP，不含路径或凭据'
  if (draft.port.trim() !== '') {
    const port = Number(draft.port.trim())
    if (!Number.isInteger(port) || port < 1 || port > 65535) return '端口必须是 1-65535 的整数'
  }
  if (/[\s]/.test(draft.username)) return '用户名不能含空白字符'
  if (/[\s]/.test(draft.password)) return '密码不能含空白字符'
  return null
}

/** The masked URL the save will store — a preview, never the stored secret. */
function displayURL(draft: ProxyDraft): string {
  const host = draft.host.trim()
  if (host === '') return ''
  const port = draft.port.trim()
  const userinfo = draft.username === '' ? '' : `${draft.username}:${draft.password === '' ? '' : '•••'}@`
  return `${draft.scheme}://${userinfo}host${port === '' ? '' : `:${port}`}`
}

/**
 * The project's own upstream proxy. Traffic of this project's sessions
 * egresses through it; sessions of other projects keep their own route (or the
 * node default), so two projects can run through different exits at the same
 * time. The enabled switch and the endpoint save together: a disabled proxy is
 * a draft the operator keeps, still validated, so flipping the switch later
 * cannot arm an address the dialer would reject. The value rides every turn as
 * a fork extension — saved settings take effect at the project's next turn, no
 * restart, and never leak into other projects.
 */
export function ProxyCard({ project, onSaved, onDirty }: {
  project: ProjectInfo
  onSaved: () => void
  onDirty: (id: string, dirty: boolean) => void
}) {
  const baseline = useRef(draftFromProject(project))
  const [draft, setDraft] = useState(baseline.current)
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const dirty = JSON.stringify(draft) !== JSON.stringify(baseline.current)

  useEffect(() => {
    const incoming = draftFromProject(project)
    const previous = baseline.current
    baseline.current = incoming
    setDraft(current => {
      if (JSON.stringify(current) !== JSON.stringify(previous)) return current
      return incoming
    })
  }, [project])
  useEffect(() => { onDirty(`${project.id}:proxy`, dirty) }, [project.id, dirty, onDirty])
  useEffect(() => () => onDirty(`${project.id}:proxy`, false), [project.id, onDirty])

  function update(mutate: (draft: ProxyDraft) => ProxyDraft) {
    setDraft(current => mutate(current))
    setError('')
    setNote('')
  }

  async function save() {
    if (lock.current) return
    const problem = problems(draft)
    if (problem) { setError(problem); return }
    lock.current = true
    setBusy(true)
    try {
      const saved = await api.updateProject(project.id, { proxy: payload(draft) })
      baseline.current = draftFromProject(saved)
      setDraft(baseline.current)
      setNote(saved.proxy?.enabled
        ? '已保存并启用。代理随每个执行回合下发，保存后从该项目的下一回合生效；其他项目不受影响。'
        : '已保存为草稿。未启用时该项目走节点默认代理，开关随下一回合同步到执行端。')
      onSaved()
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { lock.current = false; setBusy(false) }
  }

  async function clear() {
    if (lock.current) return
    lock.current = true
    setBusy(true)
    try {
      const saved = await api.updateProject(project.id, { proxy: null })
      baseline.current = draftFromProject(saved)
      setDraft(baseline.current)
      setNote('已清除。该项目回到节点默认代理，随下一回合同步到执行端。')
      onSaved()
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { lock.current = false; setBusy(false) }
  }

  return <Panel
    title="出网代理"
    extra={
      <div className="flex flex-wrap items-center justify-end gap-1.5">
        {project.proxy && <Badge variant={project.proxy.enabled ? 'secondary' : 'muted'} size="sm">{project.proxy.enabled ? '生效中' : '草稿'}</Badge>}
        {dirty && <Badge variant="warning" size="sm">未保存</Badge>}
      </div>
    }
    bodyClassName="@container space-y-4"
  >
    <div className="space-y-1 text-[12px] leading-relaxed text-ink-2">
      <p>
        本项目的流量经此代理转发：执行节点的每一回合都会带上这份配置，项目之间互不影响，
        未配置时走节点默认代理。协议、主机、端口、认证都保存在项目里，随时可改或清除。
      </p>
    </div>
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-hairline bg-surface-2/40 px-3 py-2.5">
      <div className="flex items-center gap-3">
        <Switch
          id={`proxy-enabled-${project.id}`}
          checked={draft.enabled}
          disabled={busy}
          onCheckedChange={next => update(current => ({ ...current, enabled: next }))}
        />
        <label htmlFor={`proxy-enabled-${project.id}`} className="cursor-pointer text-[12px] font-medium text-ink">
          启用本项目代理 · {draft.enabled ? '开启' : '关闭'}
        </label>
      </div>
    </div>
    <div className="grid gap-3 @min-[36rem]:grid-cols-2">
      <Field label="协议">
        <Select value={draft.scheme} disabled={busy} onValueChange={value => update(current => ({ ...current, scheme: value as EgressProxyScheme }))}>
          <SelectTrigger aria-label="代理协议"><SelectValue /></SelectTrigger>
          <SelectContent>
            {EGRESS_PROXY_SCHEMES.map(scheme => (
              <SelectItem key={scheme} value={scheme}>{SCHEME_LABELS[scheme]}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field label="主机">
        <Input value={draft.host} disabled={busy} onChange={e => update(current => ({ ...current, host: e.target.value }))} placeholder="proxy.example.com 或 10.0.0.9" aria-label="代理主机" />
      </Field>
      <Field label="端口" hint={`留空${DEFAULT_PORT[draft.scheme] ? `使用默认（${DEFAULT_PORT[draft.scheme]}）` : '则由服务端点自定'}`}>
        <Input inputMode="numeric" value={draft.port} disabled={busy} onChange={e => update(current => ({ ...current, port: e.target.value }))} placeholder={DEFAULT_PORT[draft.scheme] || '端口'} aria-label="代理端口" />
      </Field>
      <Field label="用户名" hint="代理不需要认证时两项都留空">
        <Input value={draft.username} disabled={busy} onChange={e => update(current => ({ ...current, username: e.target.value }))} autoComplete="off" aria-label="代理用户名" />
      </Field>
      <Field label="密码">
        <Input type="password" value={draft.password} disabled={busy} onChange={e => update(current => ({ ...current, password: e.target.value }))} autoComplete="new-password" aria-label="代理密码" />
      </Field>
    </div>
    {project.proxy && (
      <p className="text-xs text-ink-3">
        当前保存：<code className="break-all">{project.proxy.scheme}://{project.proxy.username ? '•••@' : ''}{project.proxy.host}{project.proxy.port ? `:${project.proxy.port}` : ''}</code>
      </p>
    )}
    {error && <Callout tone="destructive">{error}</Callout>}
    {note && <Callout tone="success">{note}</Callout>}
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" variant="soft" onClick={() => void save()} disabled={busy || !dirty} aria-busy={busy}>
        {busy && <Spinner className="size-4" />}保存
      </Button>
      {project.proxy && (
        <Button size="sm" variant="ghost" onClick={() => void clear()} disabled={busy}>清除配置</Button>
      )}
      {dirty && displayURL(draft) !== '' && <span className="text-xs text-ink-3">将保存为 {displayURL(draft)}</span>}
    </div>
    <p className="text-xs text-ink-3">
      示例：SOCKS5 · 10.0.0.9 · 1080（带认证填用户名密码）；或 Trojan · relay.example · 443。
      保存后随下一执行回合生效，无需重启节点；clash 订阅属于节点级默认代理，在主机设置里配置。
    </p>
  </Panel>
}
