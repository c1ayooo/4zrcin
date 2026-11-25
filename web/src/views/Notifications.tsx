import { useEffect, useMemo, useState } from 'react'
import { Bell, Plus, RefreshCw, Send, Trash2 } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  Checkbox,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Field,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SkeletonList,
  Spinner,
  Switch,
  Textarea,
  useConfirm,
} from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { api } from '@/lib/api'
import {
  CHANNEL_FIELDS,
  SEVERITY_OPTIONS,
  formatHeaderLines,
  formatRecipients,
  parseHeaderLines,
  parseRecipients,
  type ChannelFieldDef,
} from '@/lib/notify-fields'
import { useLoad } from '@/lib/hooks'
import type { NotifyChannel, NotifyDelivery } from '@/lib/types'

/** 编辑器里的通道草稿。config 的值保持字符串形态（掩码值原样保留），
 *  在保存前按通道字段表做形状转换（收件人数组、布尔 TLS、请求头映射）。 */
interface Draft {
  id?: string
  name: string
  kind: string
  enabled: boolean
  mode: 'realtime' | 'digest'
  rate_per_min: number
  events: string[]
  min_severity: string
  config: Record<string, string>
}

const STATE_BADGE: Record<NotifyDelivery['state'], { label: string; variant: 'success' | 'warning' | 'destructive' }> = {
  sent: { label: '已送达', variant: 'success' },
  pending: { label: '待发送', variant: 'warning' },
  failed: { label: '失败', variant: 'destructive' },
}

function emptyDraft(kind: string): Draft {
  return { name: '', kind, enabled: true, mode: 'realtime', rate_per_min: 0, events: [], min_severity: '', config: {} }
}

export function Notifications() {
  const meta = useLoad(() => api.notifyMeta(), [])
  const channels = useLoad(() => api.notifyChannels(), [])
  const deliveries = useLoad(() => api.notifyDeliveries(), [])
  const confirm = useConfirm()

  // 全局设置跟随 meta 一次初始化，之后本地编辑、显式保存。
  const [settings, setSettings] = useState<{ enabled: boolean; base_url: string; digest_minutes: number } | null>(null)
  useEffect(() => {
    if (meta.data && settings === null) {
      setSettings({ enabled: meta.data.enabled, base_url: meta.data.base_url ?? '', digest_minutes: meta.data.digest_minutes })
    }
  }, [meta.data, settings])

  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<{ tone: 'success' | 'destructive'; text: string } | null>(null)

  const eventKinds = meta.data?.event_kinds ?? []
  const channelKinds = meta.data?.channel_kinds ?? []
  const kindLabel = (id: string) => channelKinds.find((k) => k.id === id)?.label ?? id

  function openCreate() {
    setDraft(emptyDraft(channelKinds[0]?.id ?? 'dingtalk'))
  }
  function openEdit(ch: NotifyChannel) {
    const config: Record<string, string> = {}
    for (const [k, v] of Object.entries(ch.config ?? {})) {
      config[k] = Array.isArray(v) ? formatRecipients(v) : String(v ?? '')
    }
    setDraft({
      id: ch.id,
      name: ch.name,
      kind: ch.kind,
      enabled: ch.enabled,
      mode: ch.mode,
      rate_per_min: ch.rate_per_min,
      events: ch.events ?? [],
      min_severity: ch.min_severity ?? '',
      config,
    })
  }

  async function saveDraft() {
    if (!draft) return
    if (!draft.name.trim()) {
      setNote({ tone: 'destructive', text: '通道名称不能为空' })
      return
    }
    setBusy(true)
    setNote(null)
    try {
      const config = buildConfig(draft.kind, draft.config)
      if (draft.id) {
        await api.notifyUpdateChannel(draft.id, { ...draft, config })
      } else {
        await api.notifyCreateChannel({ ...draft, config })
      }
      setDraft(null)
      setNote({ tone: 'success', text: '通道已保存' })
      channels.reload()
      meta.reload()
    } catch (err) {
      setNote({ tone: 'destructive', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function removeChannel(ch: NotifyChannel) {
    const ok = await confirm({ title: `删除通道「${ch.name}」？`, description: '删除后该通道不再接收任何通知，投递历史保留。' })
    if (!ok) return
    setBusy(true)
    try {
      await api.notifyDeleteChannel(ch.id)
      channels.reload()
    } catch (err) {
      setNote({ tone: 'destructive', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function testChannel(ch: NotifyChannel) {
    setNote(null)
    setBusy(true)
    try {
      await api.notifyTestChannel(ch.id)
      setNote({ tone: 'success', text: `测试消息已发往「${ch.name}」` })
    } catch (err) {
      setNote({ tone: 'destructive', text: `测试发送失败：${err instanceof Error ? err.message : String(err)}` })
    } finally {
      setBusy(false)
    }
  }

  async function toggleChannel(ch: NotifyChannel, enabled: boolean) {
    setBusy(true)
    try {
      // 回传整条通道：服务端按「显式提交才改」处理，部分提交会清空未提交字段。
      await api.notifyUpdateChannel(ch.id, { ...ch, enabled })
      channels.reload()
    } catch (err) {
      setNote({ tone: 'destructive', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function retryDelivery(d: NotifyDelivery) {
    setBusy(true)
    try {
      await api.notifyRetryDelivery(d.id)
      deliveries.reload()
    } catch (err) {
      setNote({ tone: 'destructive', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function saveSettings() {
    if (!settings) return
    setBusy(true)
    setNote(null)
    try {
      await api.notifySaveSettings({
        enabled: settings.enabled,
        base_url: settings.base_url,
        digest_minutes: Number(settings.digest_minutes) || undefined,
      })
      setNote({ tone: 'success', text: '全局设置已保存' })
      meta.reload()
    } catch (err) {
      setNote({ tone: 'destructive', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  const stats = meta.data?.stats
  return (
    <Page
      title="通知中心"
      subtitle="把运行事件推送到钉钉、飞书、企业微信、Telegram、邮件或任意 Webhook。每个通道独立配置连接参数与接收的通知内容。"
      actions={
        <>
          <Button variant="outline" onClick={() => { channels.reload(); deliveries.reload(); meta.reload() }} disabled={channels.loading}>
            <RefreshCw aria-hidden="true" className="size-4" /> 刷新
          </Button>
          <Button onClick={openCreate} disabled={!meta.data}>
            <Plus aria-hidden="true" className="size-4" /> 添加通道
          </Button>
        </>
      }
      meta={stats ? (
        <span className="font-mono text-[11px] text-ink-3">
          待发 {stats.pending} · 已送达 {stats.sent} · 失败 {stats.failed}
          {stats.dropped > 0 ? ` · 队列溢出丢弃 ${stats.dropped}` : ''}
        </span>
      ) : null}
    >
      {note && (
        <Callout tone={note.tone === 'success' ? 'success' : 'destructive'} className="mb-3">
          {note.text}
        </Callout>
      )}
      {meta.error && (
        <Callout tone="destructive" className="mb-3">
          通知配置读取失败：{meta.error}
        </Callout>
      )}

      {settings && (
        <Panel title="全局设置" className="mb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="flex items-center gap-3">
              <Switch checked={settings.enabled} onCheckedChange={(v) => setSettings({ ...settings, enabled: v })} aria-label="启用通知推送" />
              <span className="text-[13px]">启用推送</span>
            </div>
            <Field label="汇总周期（分钟）" hint="digest 模式通道的攒批时长">
              <Input type="number" min={1} max={1440} value={settings.digest_minutes}
                onChange={(e) => setSettings({ ...settings, digest_minutes: Number(e.target.value) })} />
            </Field>
            <Field label="面板回链地址" hint="通知里「查看详情」链接的前缀；留空则不带链接" className="sm:col-span-2">
              <Input value={settings.base_url} onChange={(e) => setSettings({ ...settings, base_url: e.target.value })} placeholder="https://…" />
            </Field>
            <div>
              <Button variant="outline" onClick={saveSettings} disabled={busy}>保存设置</Button>
            </div>
          </div>
        </Panel>
      )}

      <Panel title="通知通道" extra={<span className="text-[12px] text-ink-3">{channels.data?.length ?? 0} 个通道</span>}>
        {channels.loading && !channels.data ? (
          <SkeletonList count={3} />
        ) : (channels.data?.length ?? 0) === 0 ? (
          <EmptyState icon={Bell} title="还没有通知通道"
            description="添加一个通道并勾选它要接收的通知内容，例如「漏洞入库」或「运行失败」。" />
        ) : (
          <div className="divide-y">
            {(channels.data ?? []).map((ch) => (
              <div key={ch.id} className="flex items-start justify-between gap-3 py-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2 text-[13px] font-medium">
                    {ch.name}
                    <Badge variant="outline" size="sm">{kindLabel(ch.kind)}</Badge>
                    <Badge variant={ch.mode === 'digest' ? 'info' : 'muted'} size="sm">{ch.mode === 'digest' ? '汇总' : '实时'}</Badge>
                    {!ch.enabled && <Badge variant="muted" size="sm">已停用</Badge>}
                  </div>
                  <div className="mt-1 truncate text-[12px] text-ink-3">{eventsSummary(ch, eventKinds)}</div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Switch checked={ch.enabled} onCheckedChange={(v) => toggleChannel(ch, v)} disabled={busy} aria-label={`启用通道 ${ch.name}`} />
                  <Button size="xs" variant="outline" onClick={() => testChannel(ch)} disabled={busy}>
                    <Send aria-hidden="true" className="size-3.5" /> 测试
                  </Button>
                  <Button size="xs" variant="outline" onClick={() => openEdit(ch)}>编辑</Button>
                  <Button size="xs" variant="ghost" onClick={() => removeChannel(ch)} disabled={busy} aria-label={`删除通道 ${ch.name}`}>
                    <Trash2 aria-hidden="true" className="size-3.5" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="投递历史" className="mt-4" extra={<span className="text-[12px] text-ink-3">最多保留 500 条，重启清空</span>}>
        {deliveries.loading && !deliveries.data ? (
          <SkeletonList count={3} />
        ) : (deliveries.data?.length ?? 0) === 0 ? (
          <EmptyState icon={Bell} title="暂无投递记录" description="产生事件后这里会记录每次投递的结果。" />
        ) : (
          <div className="divide-y">
            {(deliveries.data ?? []).slice(0, 100).map((d) => {
              const badge = STATE_BADGE[d.state]
              return (
                <div key={d.id} className="flex items-start justify-between gap-3 py-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2 text-[13px] font-medium">
                      <Badge variant={badge.variant} size="sm">{badge.label}</Badge>
                      {d.title}
                    </div>
                    <div className="mt-1 truncate text-[12px] text-ink-3">
                      {`${d.event_label} · ${d.channel_name}${d.project ? ` · ${d.project}` : ''} · 尝试 ${d.attempts} 次${d.last_error ? ` · ${d.last_error}` : ''}`}
                    </div>
                  </div>
                  {d.state === 'failed' && (
                    <Button size="xs" variant="outline" onClick={() => retryDelivery(d)} disabled={busy}>重试</Button>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Panel>

      {draft && (
        <ChannelDialog draft={draft} setDraft={setDraft} eventKinds={eventKinds} kindLabel={kindLabel}
          busy={busy} onSave={saveDraft} onClose={() => setDraft(null)} />
      )}
    </Page>
  )
}

function eventsSummary(ch: NotifyChannel, kinds: { id: string; label: string }[]): string {
  if (!ch.events || ch.events.length === 0) return '接收全部通知内容'
  const labels = ch.events.map((id) => kinds.find((k) => k.id === id)?.label ?? id)
  const extra = ch.min_severity
    ? `（${SEVERITY_OPTIONS.find((o) => o.value === ch.min_severity)?.label ?? ch.min_severity}）`
    : ''
  return `接收：${labels.join('、')}${extra}`
}

/** 按通道字段表把表单字符串转成后端期望的 config 形状：
 *  email 的收件人与 TLS、webhook 的自定义头是表单口径到存储口径的换算点。 */
function buildConfig(kind: string, values: Record<string, string>): Record<string, unknown> {
  const defs = CHANNEL_FIELDS[kind] ?? []
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

interface DialogProps {
  draft: Draft
  setDraft: (d: Draft) => void
  eventKinds: { id: string; label: string }[]
  kindLabel: (id: string) => string
  busy: boolean
  onSave: () => void
  onClose: () => void
}

function ChannelDialog({ draft, setDraft, eventKinds, kindLabel, busy, onSave, onClose }: DialogProps) {
  const defs = CHANNEL_FIELDS[draft.kind] ?? []
  const kindsById = useMemo(() => new Map(eventKinds.map((k) => [k.id, k.label])), [eventKinds])
  const patch = (p: Partial<Draft>) => setDraft({ ...draft, ...p })
  const setConfig = (key: string, value: string) => patch({ config: { ...draft.config, [key]: value } })

  function toggleEvent(id: string, on: boolean) {
    const next = on ? [...draft.events, id] : draft.events.filter((e) => e !== id)
    patch({ events: next })
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{draft.id ? `编辑通道 · ${draft.name}` : '添加通知通道'}</DialogTitle>
        </DialogHeader>

        <div className="grid gap-4">
          <Field label="名称">
            <Input value={draft.name} onChange={(e) => patch({ name: e.target.value })} placeholder="例如：安全告警群" />
          </Field>
          <Field label="通道类型" hint={draft.id ? '通道类型不可更改' : undefined}>
            <Select value={draft.kind} onValueChange={(v) => patch({ kind: v })} disabled={Boolean(draft.id)}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                {eventKinds.length >= 0 && Object.keys(CHANNEL_FIELDS).map((k) => (
                  <SelectItem key={k} value={k}>{kindLabel(k)}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>

        <div className="grid gap-4">
          {defs.map((def) => (
            <ConfigField key={def.key} def={def} value={draft.config[def.key] ?? ''} onChange={(v) => setConfig(def.key, v)} />
          ))}

          <Field label="接收的通知内容" hint="勾选该通道要接收的事件类型；全部不勾=接收全部">
            <div className="grid gap-2 rounded-md border p-3 sm:grid-cols-2">
              {eventKinds.map((k) => (
                <label key={k.id} className="flex items-center gap-2 text-[13px]">
                  <Checkbox
                    checked={draft.events.includes(k.id)}
                    onCheckedChange={(v) => toggleEvent(k.id, v === true)}
                    aria-label={`接收 ${k.label}`}
                  />
                  {k.label}
                </label>
              ))}
            </div>
          </Field>

          {draft.events.includes('vuln_recorded') && (
            <Field label="漏洞级别门槛" hint="仅对「漏洞入库」生效；无级别语义的事件不受影响">
              <Select value={draft.min_severity} onValueChange={(v) => patch({ min_severity: v })}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {SEVERITY_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}

          <div className="grid grid-cols-2 gap-4">
            <Field label="投递模式" hint="实时=逐条即发；汇总=攒批合并">
              <Select value={draft.mode} onValueChange={(v) => patch({ mode: v as Draft['mode'] })}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="realtime">实时</SelectItem>
                  <SelectItem value="digest">汇总</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="限流（条/分钟）" hint="0=使用该渠道的默认限流；Webhook 默认不限">
              <Input type="number" min={0} value={draft.rate_per_min}
                onChange={(e) => patch({ rate_per_min: Number(e.target.value) || 0 })} />
            </Field>
          </div>
          <div className="flex items-center gap-3">
            <Switch checked={draft.enabled} onCheckedChange={(v) => patch({ enabled: v })} aria-label="启用通道" />
            <span className="text-[13px]">启用通道</span>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>取消</Button>
          <Button onClick={onSave} disabled={busy}>
            {busy && <Spinner className="size-3.5" />} 保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConfigField({ def, value, onChange }: { def: ChannelFieldDef; value: string; onChange: (v: string) => void }) {
  const common = { value, onChange: (e: { target: { value: string } }) => onChange(e.target.value), placeholder: def.placeholder }
  return (
    <Field label={def.label} hint={def.hint}>
      {def.type === 'textarea' ? (
        <Textarea rows={3} {...common} />
      ) : def.type === 'select' ? (
        <Select value={value || def.options?.[0]?.value || ''} onValueChange={onChange}>
          <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
          <SelectContent>
            {(def.options ?? []).map((o) => (
              <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input type={def.type === 'password' ? 'password' : def.type === 'number' ? 'number' : 'text'} {...common} />
      )}
    </Field>
  )
}
