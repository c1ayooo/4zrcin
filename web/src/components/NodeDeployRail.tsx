// The deploy rail of the nodes page (docs/15 §10.4, §7.2): the three ways to
// add a node — command, SSH and direct — plus the two build recipes, all on
// this page. It is presentational: the page owns polling and the api calls, so
// the rail renders the same with a stubbed api in tests.
import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  Field,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Separator,
  Textarea,
  ToggleGroup,
  ToggleGroupItem,
} from '@cyber/ui'
import { cn } from '@cyber/theme'
import type { CreatedEnrollment, DeployJob, DistStatus, EnrollmentView, SshDeployRequest } from '@/lib/types'

export type DeployMode = 'command' | 'ssh' | 'direct'
export type Recipe = 'full' | 'capture'

export interface NodeDeployRailProps {
  /** The public ingress is on (--public-url). */
  enabled: boolean
  dist?: DistStatus
  enrollments: EnrollmentView[]
  /** The create response is the only place a token appears; the page keeps it. */
  onCreate: (options: { name?: string; recipe: Recipe; ttl_minutes?: number }) => Promise<CreatedEnrollment>
  onJoinCommand: () => Promise<string>
  /** The live SSH deployment, polled by the page. */
  deployJob?: DeployJob | null
  onStartDeploy: (request: SshDeployRequest) => Promise<void>
  onResetDeploy: () => void
}

const RECIPES: { key: Recipe; label: string; hint: string; command: string; available?: (d?: DistStatus) => boolean }[] = [
  {
    key: 'full',
    label: 'scan-full',
    hint: '全引擎',
    command: 'go build -o cyber-scan ./cmd/cyber-scan',
    available: (d) => d?.scan_full ?? true,
  },
  {
    key: 'capture',
    label: 'scan-capture',
    hint: '仅流量代理与围栏',
    command: "go build -tags 'emptyresources captureonly' -o cyber-scan-capture ./cmd/cyber-scan",
    available: (d) => d?.scan_capture ?? false,
  },
]

// copyText is best effort: the command is visible either way, and a browser
// without the async clipboard API must not turn a copy into an error page.
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

function CopyButton({ text, label = '复制', disabled, title }: { text: string; label?: string; disabled?: boolean; title?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      size="xs"
      variant="ghost"
      disabled={disabled}
      title={title}
      aria-label={label}
      onClick={async () => {
        if (await copyText(text)) {
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1200)
        }
      }}
    >
      {copied ? <Check className="h-3.5 w-3.5" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
      <span aria-live="polite">{copied ? '已复制' : label}</span>
    </Button>
  )
}

const STEP_MARK: Record<string, string> = { pending: '·', running: '›', done: '✓', failed: '✗' }

function DeployProgress({ job }: { job: DeployJob }) {
  const status =
    job.state === 'succeeded' ? '已回连' : job.state === 'failed' ? '部署失败' : '部署中…'
  return (
    <div className="space-y-2" role="group" aria-label="SSH 部署进度">
      <ol className="space-y-1">
        {job.steps.map((step) => (
          <li key={step.key} className="flex items-baseline gap-2 text-[11px] leading-4">
            <span
              className={cn(
                'w-3 shrink-0 font-mono',
                step.state === 'done' && 'text-success',
                step.state === 'failed' && 'text-destructive',
                step.state === 'running' && 'text-ink',
                step.state === 'pending' && 'text-ink-3',
              )}
              aria-hidden="true"
            >
              {STEP_MARK[step.state] ?? '·'}
            </span>
            <span className={cn('shrink-0', step.state === 'pending' ? 'text-ink-3' : 'text-ink-2')}>{step.label}</span>
            {step.detail && <span className="min-w-0 truncate font-mono text-ink-3">{step.detail}</span>}
          </li>
        ))}
      </ol>
      <p role="status" className="text-xs text-ink-3">
        {status}
      </p>
      {job.error && <Callout tone="destructive">{job.error}</Callout>}
      {job.log.length > 0 && (
        <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-md bg-surface-2 p-2 font-mono text-[11px] leading-4 text-ink-2" aria-live="polite">
          {job.log.map((line) => `${line.text}\n`).join('')}
        </pre>
      )}
    </div>
  )
}

export function NodeDeployRail({
  enabled,
  dist,
  enrollments,
  onCreate,
  onJoinCommand,
  deployJob,
  onStartDeploy,
  onResetDeploy,
}: NodeDeployRailProps) {
  // A live job belongs to the SSH page: reopening the page with one running
  // must not hide its progress behind another tab.
  const [mode, setMode] = useState<DeployMode>(deployJob ? 'ssh' : enabled ? 'command' : 'direct')
  const [name, setName] = useState('')
  const [recipe, setRecipe] = useState<Recipe>('full')
  const [ttl, setTtl] = useState('60')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [created, setCreated] = useState<CreatedEnrollment | null>(null)
  const [direct, setDirect] = useState('')

  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [user, setUser] = useState('root')
  const [auth, setAuth] = useState<'password' | 'key'>('password')
  const [password, setPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [sudoPassword, setSudoPassword] = useState('')

  const active = mode === 'command' ? created : direct
  const command = mode === 'command' ? created?.install_command ?? '' : direct
  const waiting =
    mode === 'command' && created ? enrollments.find((e) => e.id === created.id)?.state === 'pending' : false
  const deploying = Boolean(deployJob && deployJob.state === 'running')

  async function submit() {
    setBusy(true)
    setError('')
    try {
      if (mode === 'direct') {
        setDirect(await onJoinCommand())
      } else if (mode === 'ssh') {
        if (!host.trim()) throw new Error('需要主机地址')
        await onStartDeploy({
          host: host.trim(),
          port: Number(port) || 22,
          user: user.trim() || 'root',
          ...(auth === 'password'
            ? { password }
            : { private_key: privateKey, passphrase: passphrase || undefined }),
          ...(sudoPassword ? { sudo_password: sudoPassword } : {}),
          name: name.trim() || undefined,
        })
        // Credentials are one-shot: do not keep them in the form after they
        // have been handed to the job.
        setPassword('')
        setPrivateKey('')
        setPassphrase('')
        setSudoPassword('')
      } else {
        const result = await onCreate({
          name: name.trim() || undefined,
          recipe,
          ttl_minutes: Number(ttl),
        })
        setCreated(result)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {!enabled && (
        <Callout tone="warning">
          <div className="space-y-1 text-xs">
            <p>节点接入未开启：启动 zrcin 时加 `--public-url https://你的域名`。</p>
            <p>未开启时只有「直连」与构建配方可用。</p>
          </div>
        </Callout>
      )}

      <section aria-label="部署" className="space-y-3">
        <ToggleGroup
          value={mode}
          ariaLabel="接入方式"
          onValueChange={(value) => {
            setMode(value as DeployMode)
            setError('')
          }}
          className="w-full"
        >
          <ToggleGroupItem value="command" disabled={!enabled} className="flex-1">
            命令部署
          </ToggleGroupItem>
          <ToggleGroupItem value="ssh" disabled={!enabled} className="flex-1">
            SSH 部署
          </ToggleGroupItem>
          <ToggleGroupItem value="direct" className="flex-1">
            直连
          </ToggleGroupItem>
        </ToggleGroup>

        {mode === 'command' && (
          <>
            <Field label="名称" hint="留空自动生成">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="edge-01" />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="配方">
                <Select value={recipe} onValueChange={(v) => setRecipe(v as Recipe)}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="full">scan-full</SelectItem>
                    <SelectItem value="capture" disabled={!(dist?.scan_capture ?? false)}>
                      scan-capture{dist && !dist.scan_capture ? '（缺产物）' : ''}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="有效期">
                <Select value={ttl} onValueChange={setTtl}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="15">15 分钟</SelectItem>
                    <SelectItem value="60">60 分钟</SelectItem>
                    <SelectItem value="1440">24 小时</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
          </>
        )}

        {mode === 'ssh' && !deployJob && (
          <>
            <div className="grid grid-cols-[1fr_5rem] gap-2">
              <Field label="主机">
                <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="10.0.0.5" autoComplete="off" />
              </Field>
              <Field label="端口">
                <Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" />
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label="用户">
                <Input value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" />
              </Field>
              <Field label="认证">
                <Select value={auth} onValueChange={(v) => setAuth(v as 'password' | 'key')}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="password">密码</SelectItem>
                    <SelectItem value="key">私钥</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            {auth === 'password' ? (
              <Field label="密码">
                <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
              </Field>
            ) : (
              <>
                <Field label="私钥" hint="OpenSSH 或 PEM">
                  <Textarea
                    value={privateKey}
                    onChange={(e) => setPrivateKey(e.target.value)}
                    rows={4}
                    className="font-mono text-[11px]"
                    spellCheck={false}
                  />
                </Field>
                <Field label="私钥口令" hint="没有就留空">
                  <Input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} autoComplete="new-password" />
                </Field>
              </>
            )}
            {user.trim() !== 'root' && (
              <Field label="sudo 口令" hint="免密 sudo 可留空">
                <Input type="password" value={sudoPassword} onChange={(e) => setSudoPassword(e.target.value)} autoComplete="new-password" />
              </Field>
            )}
            <Field label="名称" hint="留空自动生成">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="edge-01" />
            </Field>
          </>
        )}

        {mode === 'direct' && (
          <p className="text-xs leading-relaxed text-ink-2">
            执行机须与控制台共享项目目录，并能访问 Hub 与控制台 API。
          </p>
        )}

        {error && <Callout tone="destructive">{error}</Callout>}

        {deployJob && mode === 'ssh' ? (
          <div className="space-y-3">
            <DeployProgress job={deployJob} />
            {deployJob.state !== 'running' && (
              <Button size="sm" variant="outline" onClick={onResetDeploy}>
                再次部署
              </Button>
            )}
          </div>
        ) : (
          <Button size="sm" disabled={busy || deploying || (mode !== 'direct' && !enabled)} onClick={() => void submit()}>
            {busy ? '处理中…' : mode === 'ssh' ? '开始部署' : '生成接入命令'}
          </Button>
        )}

        {mode === 'command' && active && command && (
          <div className="space-y-2">
            <pre className="zrcin-break whitespace-pre-wrap rounded-md bg-surface-2 p-2 font-mono text-[11px] leading-4 text-ink">
              {command}
            </pre>
            <div className="flex items-center justify-between gap-2">
              <span role="status" className="text-xs text-ink-3">
                {waiting ? '等待节点回连…' : '已回连'}
              </span>
              <CopyButton text={command} disabled={!command} />
            </div>
          </div>
        )}

        {mode === 'direct' && active && command && (
          <div className="space-y-2">
            <pre className="zrcin-break whitespace-pre-wrap rounded-md bg-surface-2 p-2 font-mono text-[11px] leading-4 text-ink">
              {command}
            </pre>
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-ink-3">在执行机上运行</span>
              <CopyButton text={command} disabled={!command} />
            </div>
          </div>
        )}

        {mode === 'direct' && <Separator />}
        {mode === 'command' && created && (
          <p className="text-[11px] leading-4 text-ink-3">命令只在生成时显示一次；重新生成等于发一条新的接入记录。</p>
        )}
      </section>

      <Separator />

      <section aria-label="构建配方" className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs font-medium text-ink-2">构建配方</span>
          <span className="font-mono text-[11px] text-ink-3" aria-label="产物状态">
            {['启动器', 'scan-full', 'scan-capture'].map((label, i) => {
              const ok = i === 0 ? dist?.launcher ?? false : i === 1 ? dist?.scan_full ?? true : dist?.scan_capture ?? false
              return (
                <span key={label} className={cn('mr-2 last:mr-0', ok ? 'text-success' : 'text-ink-3')}>
                  {label} {ok ? '✓' : '✗'}
                </span>
              )
            })}
          </span>
        </div>
        {RECIPES.map((r) => (
          <div key={r.key} className="space-y-1">
            <div className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-2">
                <Badge size="sm" variant="muted">
                  {r.label}
                </Badge>
                <span className="text-[11px] text-ink-3">{r.hint}</span>
              </span>
              <CopyButton text={r.command} disabled={!(r.available?.(dist) ?? true)} />
            </div>
            <pre className="zrcin-break whitespace-pre-wrap font-mono text-[11px] leading-4 text-ink-3">{r.command}</pre>
          </div>
        ))}
      </section>

      {waiting && (
        <p role="status" aria-live="polite" className="text-[11px] leading-4 text-ink-3">
          等待节点回连；回连成功后它会出现在左侧「远程节点」。
        </p>
      )}
    </div>
  )
}
