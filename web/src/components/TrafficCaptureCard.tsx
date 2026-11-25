import { useEffect, useRef, useState } from 'react'
import { KeyRound, ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react'
import { Badge, Button, Callout, Field, Input, useConfirm } from '@cyber/ui'
import { Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { settingsApplyNotice, settingsModelPayload } from '@/lib/settings'
import type { AsyncState } from '@/lib/hooks'
import type { SettingsView, TrafficCaptureSummary } from '@/lib/types'

const CAPTURE_MODES: { id: 'relay' | 'record'; label: string; hint: string }[] = [
  { id: 'relay', label: 'relay（透传）', hint: '只记录元数据，不拦截、不解密；HTTPS 正文保持密文。' },
  { id: 'record', label: 'record（拦截 + MITM）', hint: '拦截并记录请求响应；配合 HTTPS 解密可看到明文正文。' },
]

interface CaptureDraft {
  mode: 'relay' | 'record'
  decrypt: boolean
  caPath: string
}

function draftOf(capture: TrafficCaptureSummary): CaptureDraft {
  return {
    mode: capture.capture_mode === 'record' ? 'record' : 'relay',
    decrypt: capture.decrypt_https,
    caPath: capture.ca_cert_path || '',
  }
}

function draftEquals(a: CaptureDraft, b: CaptureDraft): boolean {
  return a.mode === b.mode && a.decrypt === b.decrypt && a.caPath === b.caPath
}

function draftProblem(draft: CaptureDraft): string {
  if (draft.decrypt && draft.mode !== 'record') return 'HTTPS 解密需要 capture_mode=record：relay 透传模式无法解密。'
  if (draft.decrypt && !draft.caPath.trim()) return '启用 HTTPS 解密必须提供 CA 证书路径。'
  return ''
}

/**
 * The operator's `extensions.traffic` capture settings — writable (P4).
 *
 * MITM is executed by the harness proxy; the console never starts it and never
 * issues certificates. The card keeps the honest read answers ("will HTTPS
 * appear in plaintext?", "which certificate is the browser asking about?") and
 * adds the write path with the same closed key set the scan's strict decoder
 * accepts. Saving rides PUT /api/settings with the model half (a product-only
 * save would clear the gateway), and the section is read at scan start, so the
 * response's restart notice is part of the contract, not an error.
 */
export function TrafficCaptureCard({ capture, settings }: {
  capture?: TrafficCaptureSummary
  settings: AsyncState<SettingsView>
}) {
  // An older server has no capture projection: render nothing instead of
  // inventing a state that could be wrong.
  if (!capture) return null
  return <CaptureEditor capture={capture} settings={settings} />
}

function CaptureEditor({ capture, settings }: {
  capture: TrafficCaptureSummary
  settings: AsyncState<SettingsView>
}) {
  const confirm = useConfirm()
  const [draft, setDraft] = useState<CaptureDraft>(() => draftOf(capture))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [apply, setApply] = useState<{ tone: 'success' | 'warning' | 'info'; text: string } | null>(null)
  const baseline = useRef<CaptureDraft>()
  const lock = useRef(false)
  useEffect(() => {
    const next = draftOf(capture)
    const old = baseline.current
    baseline.current = next
    setDraft(current => (old === undefined || draftEquals(current, old)) ? next : current)
  }, [capture])
  const dirty = !!baseline.current && !draftEquals(draft, baseline.current)
  const caChanged = !!baseline.current && draft.caPath.trim() !== baseline.current.caPath

  async function save() {
    if (lock.current || !settings.data || !dirty) return
    const problem = draftProblem(draft)
    if (problem) {
      setError(problem)
      return
    }
    // Enabling decryption or pointing at a different CA changes what every
    // proxied client must trust: confirm before the document changes.
    if (draft.decrypt || (caChanged && !!draft.caPath.trim())) {
      const ok = await confirm({
        title: '确认启用 HTTPS 解密',
        description:
          '启用解密或更换 CA 后，所有经过代理的客户端（浏览器、命令行工具、目标程序）必须重新信任新证书，否则 HTTPS 请求会失败或被拒绝。\n\n保存成功后请核对新指纹（sha256:…），并与浏览器信任提示中显示的指纹比对一致后再继续工作。',
        confirmLabel: '确认并保存',
      })
      if (!ok) return
    }
    await persist()
  }

  async function persist() {
    if (lock.current || !settings.data) return
    lock.current = true; setBusy(true); setError(''); setApply(null)
    try {
      // A PUT applies the whole settings view, so the model half rides along;
      // the capture section is the one added field. It never echoes back —
      // the refreshed projection is the source of truth.
      const next = await api.saveSettings({
        ...settingsModelPayload(settings.data),
        traffic_capture_write: {
          capture_mode: draft.mode,
          decrypt_https: draft.decrypt,
          ...(draft.caPath.trim() ? { ca_cert: draft.caPath.trim() } : {}),
        },
      })
      settings.setData(next)
      baseline.current = draftOf(next.traffic_capture || capture)
      setDraft(baseline.current)
      setApply(settingsApplyNotice(next))
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) } finally { lock.current = false; setBusy(false) }
  }

  const recording = draft.mode === 'record'
  return (
    <Panel
      title="流量捕获"
      extra={<Badge variant="outline" size="sm">cyber.yaml extensions.traffic · scan 启动时读取</Badge>}
      bodyClassName="space-y-3"
      data-testid="traffic-capture"
    >
      <div className="flex flex-wrap items-center gap-2">
        {recording ? (
          <ShieldCheck className="h-4 w-4 text-success" aria-hidden="true" />
        ) : (
          <ShieldOff className="h-4 w-4 text-ink-3" aria-hidden="true" />
        )}
        <Badge size="sm" variant={recording ? 'success' : 'muted'}>
          {recording ? 'record（拦截 + MITM）' : 'relay（默认透传）'}
        </Badge>
        {draft.decrypt && recording && <Badge size="sm" variant="info">HTTPS 解密已启用</Badge>}
      </div>

      {capture.reason && !dirty && (
        <Callout tone="warning" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
          {capture.reason}
        </Callout>
      )}

      <fieldset className="min-w-0 space-y-2.5" disabled={busy}>
        <legend className="text-caption font-medium text-ink-2">捕获模式</legend>
        <div className="flex flex-wrap gap-1.5">
          {CAPTURE_MODES.map(mode => (
            <Button
              key={mode.id}
              type="button"
              size="sm"
              variant={draft.mode === mode.id ? 'default' : 'outline'}
              aria-pressed={draft.mode === mode.id}
              data-capture-mode={mode.id}
              onClick={() => setDraft(d => ({ ...d, mode: mode.id }))}
            >
              {mode.label}
            </Button>
          ))}
        </div>
        <p className="text-[11px] leading-4 text-ink-3">{CAPTURE_MODES.find(m => m.id === draft.mode)?.hint}</p>

        <label className="flex items-center gap-2 text-[12px] text-ink-2">
          <input
            type="checkbox"
            className="h-3.5 w-3.5 accent-[var(--primary)]"
            checked={draft.decrypt}
            aria-label="解密 HTTPS（decrypt_https）"
            onChange={e => setDraft(d => ({ ...d, decrypt: e.target.checked }))}
          />
          解密 HTTPS（decrypt_https）——仅 record 模式生效
        </label>

        <Field
          label="CA 证书路径（ca_cert）"
          hint="证书由操作员生成并自行加入系统/浏览器信任（指纹须与下方一致）；控制台不代发证书、不存储证书文件，只读取并展示指纹。"
        >
          <Input
            value={draft.caPath}
            onChange={e => setDraft(d => ({ ...d, caPath: e.target.value }))}
            aria-label="CA 证书路径"
            spellCheck={false}
            autoCapitalize="none"
            placeholder="/path/to/ca.pem"
            className="font-mono text-[12px]"
          />
        </Field>

        <dl className="grid grid-cols-1 gap-x-4 gap-y-1.5 text-[12px] leading-relaxed">
          <div className="flex min-w-0 items-baseline gap-1.5">
            <dt className="flex shrink-0 items-center gap-1 text-ink-3"><KeyRound className="h-3 w-3" aria-hidden="true" />当前 CA 指纹</dt>
            <dd className="tw-break min-w-0 font-mono text-[11px] text-ink-2">
              {capture.ca_fingerprint || (capture.ca_cert_path ? '无法读取证书文件' : '未配置')}
            </dd>
          </div>
        </dl>

        {error && (
          <p className="text-[12px] text-destructive" role="alert">{error}</p>
        )}
        {apply && <Callout tone={apply.tone}>{apply.text}</Callout>}

        <div className="flex flex-wrap items-center gap-2 border-t border-hairline pt-2.5">
          <Button
            type="button"
            size="sm"
            disabled={!dirty || busy}
            data-testid="traffic-capture-save"
            onClick={() => { void save() }}
          >
            保存捕获配置
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={!dirty || busy}
            onClick={() => { if (baseline.current) { setDraft(baseline.current); setError('') } }}
          >
            重置
          </Button>
          <span className="text-[11px] text-ink-3">
            保存后写入 cyber.yaml；scan 在启动时读取该段，需重启本机宿主后生效。
          </span>
        </div>
      </fieldset>
    </Panel>
  )
}
