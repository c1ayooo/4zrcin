// 执行环境 · WebShell (docs/18): the shell ledger on the left, the operations
// against the selected shell on the right. Every operation runs on the shell's
// node over the directive channel — the target sees that host, not the console.
import { useState } from 'react'
import { FileCode2, RefreshCw, ShieldAlert, Trash2 } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  EmptyState,
  Field,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SkeletonList,
  StatusDot,
  Textarea,
  useConfirm,
} from '@cyber/ui'
import { cn } from '@cyber/theme'
import { Page, Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { useLoad, usePoll } from '@/lib/hooks'
import { describeShellResult, encodeShellWrite, outputPreview, protocolLabel, shellNodeLabel, shellStatusView } from '@/lib/shells'
import type { ShellOp, ShellOpResult, ShellRegistration, ShellView } from '@/lib/types'

const TYPE_LABEL: Record<string, string> = { php: 'PHP', jsp: 'JSP', aspx: 'ASPX' }

export function Shells() {
  const shells = usePoll(api.shells, 4000)
  const [selected, setSelected] = useState<string | null>(null)
  const [registering, setRegistering] = useState(false)

  const rows = shells.data?.shells ?? []
  const current = rows.find((s) => s.id === selected) ?? null

  return (
    <Page
      title="WebShell"
      scroll={false}
      className="@container"
      actions={
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => shells.reload()} disabled={shells.loading || shells.refreshing}>
            <RefreshCw className={cn('h-3.5 w-3.5', shells.refreshing && 'motion-safe:animate-spin')} aria-hidden="true" />
            刷新
          </Button>
          <Button size="sm" onClick={() => setRegistering(true)}>
            登记
          </Button>
        </div>
      }
    >
      <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto overscroll-contain py-1 @4xl:flex-row @4xl:overflow-hidden">
        <div className="min-w-0 flex-none space-y-3 @4xl:flex-1 @4xl:overflow-y-auto @4xl:pr-1">
          {shells.error && <Callout tone="destructive">{shells.error}</Callout>}
          {shells.loading && (
            <Panel bodyClassName="p-4">
              <SkeletonList count={3} />
            </Panel>
          )}
          {!shells.loading && rows.length === 0 && (
            <Panel bodyClassName="p-0">
              <EmptyState icon={FileCode2} title="没有受管 webshell" description="登记一个壳：新建（平台生成文件与密钥）或接入目标上已有的壳。" />
            </Panel>
          )}
          {rows.length > 0 && (
            <Panel
              title="受管壳"
              extra={<span className="zrcin-nums font-mono text-[11px] text-ink-3">{rows.length} 个</span>}
              bodyClassName="p-0"
            >
              <ul role="list" className="divide-y divide-hairline" aria-label="受管 webshell">
                {rows.map((sh) => {
                  const status = shellStatusView(sh.status)
                  const active = current?.id === sh.id
                  const preview = outputPreview(sh.last_output)
                  return (
                    <li key={sh.id}>
                      <button
                        type="button"
                        onClick={() => {
                          setSelected(sh.id)
                          setRegistering(false)
                        }}
                        aria-current={current?.id === sh.id ? 'true' : undefined}
                        className={cn(
                          'flex w-full items-start gap-2.5 px-3 py-2.5 text-left transition-colors',
                          active ? 'bg-primary/[0.09] shadow-[inset_2px_0_0_var(--primary)]' : 'hover:bg-surface-2',
                        )}
                      >
                        <StatusDot status={status.dot} className="mt-1.5" />
                        <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                            <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">{sh.name}</span>
                            <span className={cn('text-xs', toneClass(status.tone))}>{status.label}</span>
                            <span className="font-mono text-[11px] text-ink-3">
                              {TYPE_LABEL[sh.type] || sh.type} · {protocolLabel(sh.protocol)}
                            </span>
                            <Badge size="sm" variant={sh.source === 'model' ? 'info' : 'outline'}>
                              {sh.source === 'model' ? '模型登记' : '操作员登记'}
                            </Badge>
                          </span>
                          <span className="zrcin-break mt-0.5 block font-mono text-[11px] leading-4 text-ink-2">{sh.url}</span>
                          {preview && <span className="zrcin-break mt-0.5 block truncate font-mono text-[11px] leading-4 text-ink-3">{preview}</span>}
                        </span>
                      </button>
                    </li>
                  )
                })}
              </ul>
            </Panel>
          )}
        </div>

        <div className="min-w-0 flex-none pb-1 @4xl:w-[30rem]">
          {registering ? (
            <RegisterPanel
              onClose={() => setRegistering(false)}
              onCreated={(created) => {
                setRegistering(false)
                setSelected(created.shell.id)
                shells.reload()
              }}
            />
          ) : current ? (
            <ShellDetail
              shell={current}
              onChanged={() => shells.reload()}
              onForgotten={() => {
                setSelected(null)
                shells.reload()
              }}
            />
          ) : (
            <Panel bodyClassName="p-4">
              <EmptyState icon={ShieldAlert} title="选择一个壳" description="左侧列表里的每个壳都能执行命令、管理文件、自删除。" />
            </Panel>
          )}
        </div>
      </div>
    </Page>
  )
}

function toneClass(tone: 'success' | 'warning' | 'destructive' | 'muted'): string {
  switch (tone) {
    case 'success':
      return 'text-success'
    case 'warning':
      return 'text-warning'
    case 'destructive':
      return 'text-destructive'
    default:
      return 'text-ink-3'
  }
}

interface LogEntry {
  id: number
  label: string
  text: string
  failed: boolean
  at: number
}

function ShellDetail({ shell, onChanged, onForgotten }: { shell: ShellView; onChanged: () => void; onForgotten: () => void }) {
  const confirm = useConfirm()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [log, setLog] = useState<LogEntry[]>([])
  const [seq, setSeq] = useState(0)
  const [command, setCommand] = useState('')
  const [path, setPath] = useState('')
  const [writePath, setWritePath] = useState('')
  const [writeData, setWriteData] = useState('')

  const status = shellStatusView(shell.status)

  async function run(op: ShellOp, label: string) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const res: ShellOpResult = await api.runShellOp(shell.id, { op })
      const next = seq + 1
      setSeq(next)
      setLog((prev) => [{ id: next, label, text: describeShellResult(res), failed: Boolean(res.error), at: Date.now() }, ...prev].slice(0, 30))
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    const ok = await confirm({
      title: '让这个壳自删除？',
      description: `${shell.url} 会经绑定节点执行自删除并复核消失；legacy 协议无法自删除。`,
      confirmLabel: '自删除',
      destructive: true,
    })
    if (ok) await run({ kind: 'remove' }, '自删除')
  }

  async function forget() {
    const ok = await confirm({
      title: '从台账删除这一行？',
      description: '只删除记录；目标上的文件不受影响。',
      confirmLabel: '删除记录',
      destructive: true,
    })
    if (!ok) return
    setBusy(true)
    setError('')
    try {
      await api.forgetShell(shell.id)
      onForgotten()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel
      title={shell.name}
      extra={
        <span className="flex items-center gap-2">
          <StatusDot status={status.dot} />
          <span className={cn('text-xs', toneClass(status.tone))}>{status.label}</span>
        </span>
      }
      bodyClassName="space-y-3"
    >
      <dl className="grid grid-cols-1 gap-x-4 gap-y-1 text-[12px] leading-relaxed">
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="w-14 shrink-0 text-ink-3">地址</dt>
          <dd className="zrcin-break min-w-0 font-mono text-[11px] text-ink-2">{shell.url}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="w-14 shrink-0 text-ink-3">节点</dt>
          <dd className="zrcin-break min-w-0 font-mono text-[11px] text-ink-2">{shellNodeLabel(shell.node_id)}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="w-14 shrink-0 text-ink-3">协议</dt>
          <dd className="min-w-0 font-mono text-[11px] text-ink-2">
            {TYPE_LABEL[shell.type] || shell.type} · {protocolLabel(shell.protocol)}
          </dd>
        </div>
        {shell.last_checked && (
          <div className="flex min-w-0 items-baseline gap-1.5">
            <dt className="w-14 shrink-0 text-ink-3">最近检查</dt>
            <dd className="min-w-0 font-mono text-[11px] text-ink-2">{new Date(shell.last_checked).toLocaleString()}</dd>
          </div>
        )}
        {shell.note && (
          <div className="flex min-w-0 items-baseline gap-1.5">
            <dt className="w-14 shrink-0 text-ink-3">备注</dt>
            <dd className="zrcin-break min-w-0 text-[11px] text-ink-2">{shell.note}</dd>
          </div>
        )}
      </dl>

      <div className="flex flex-wrap gap-1.5">
        <Button size="xs" variant="outline" disabled={busy} onClick={() => void run({ kind: 'ping' }, '存活检查')}>
          存活检查
        </Button>
        <Button size="xs" variant="outline" disabled={busy} onClick={() => void run({ kind: 'info' }, '信息')}>
          信息
        </Button>
        <Button size="xs" variant="outline" disabled={busy || shell.status === 'removed'} onClick={() => void remove()}>
          <Trash2 className="h-3 w-3" aria-hidden="true" />
          自删除
        </Button>
        <Button size="xs" variant="ghost" disabled={busy} onClick={() => void forget()}>
          删除记录
        </Button>
      </div>

      <div className="flex items-center gap-1.5">
        <Input
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) void run({ kind: 'exec', command }, command || '命令')
          }}
          aria-label="命令"
          placeholder="id"
          spellCheck={false}
          className="font-mono text-[12px]"
          disabled={busy}
        />
        <Button size="sm" disabled={busy || !command.trim()} onClick={() => void run({ kind: 'exec', command }, command)}>
          执行
        </Button>
      </div>

      <div className="flex items-center gap-1.5">
        <Input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing && path.trim()) void run({ kind: 'files', file: 'list', path }, `列出 ${path}`)
          }}
          aria-label="目录路径"
          placeholder="/var/www"
          spellCheck={false}
          className="font-mono text-[12px]"
          disabled={busy}
        />
        <Button size="sm" variant="outline" disabled={busy || !path.trim()} onClick={() => void run({ kind: 'files', file: 'list', path }, `列出 ${path}`)}>
          列出
        </Button>
        <Button size="sm" variant="outline" disabled={busy || !path.trim()} onClick={() => void run({ kind: 'files', file: 'read', path }, `读取 ${path}`)}>
          读取
        </Button>
      </div>

      <details className="rounded-md border border-hairline px-2.5 py-2">
        <summary className="cursor-pointer text-[12px] text-ink-2">写入文件</summary>
        <div className="mt-2 space-y-2">
          <Input
            value={writePath}
            onChange={(e) => setWritePath(e.target.value)}
            aria-label="写入路径"
            placeholder="/var/www/new.txt"
            spellCheck={false}
            className="font-mono text-[12px]"
            disabled={busy}
          />
          <Textarea
            value={writeData}
            onChange={(e) => setWriteData(e.target.value)}
            aria-label="写入内容"
            placeholder="文件内容（文本）"
            className="min-h-[96px] font-mono text-[12px]"
            disabled={busy}
          />
          <Button
            size="xs"
            variant="outline"
            disabled={busy || !writePath.trim() || writeData === ''}
            onClick={() => void run({ kind: 'files', file: 'write', path: writePath, start: true, data: encodeShellWrite(writeData) }, `写入 ${writePath}`)}
          >
            覆盖写入
          </Button>
        </div>
      </details>

      {error && (
        <p className="text-[12px] text-destructive" role="alert">
          {error}
        </p>
      )}

      {log.length > 0 && (
        <div className="space-y-2 border-t border-hairline pt-2">
          {log.map((entry) => (
            <div key={entry.id} className="space-y-1">
              <div className="flex items-center justify-between gap-2">
                <span className="zrcin-break font-mono text-[11px] text-ink-3">
                  {entry.label} · {new Date(entry.at).toLocaleTimeString()}
                </span>
                {entry.failed && (
                  <Badge size="sm" variant="destructive">
                    失败
                  </Badge>
                )}
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => setLog((prev) => prev.filter((item) => item.id !== entry.id))}
                  aria-label="清除这一条结果"
                >
                  清除
                </Button>
              </div>
              <pre className="zrcin-break max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-surface-2/50 p-2 font-mono text-[11px] leading-4 text-ink-2">
                {entry.text}
              </pre>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

function RegisterPanel({ onClose, onCreated }: { onClose: () => void; onCreated: (created: ShellRegistration) => void }) {
  const nodes = useLoad(() => api.nodes(), [])
  const [url, setUrl] = useState('')
  const [type, setType] = useState<'php' | 'jsp' | 'aspx'>('php')
  const [protocol, setProtocol] = useState<'v2' | 'legacy'>('v2')
  const [adopt, setAdopt] = useState(false)
  const [password, setPassword] = useState('')
  const [key, setKey] = useState('')
  const [carrier, setCarrier] = useState('')
  // '__local__' is the sentinel the Select needs: an empty vantage means the
  // console's own host, and it is the default.
  const [nodeID, setNodeID] = useState('__local__')
  const [name, setName] = useState('')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [created, setCreated] = useState<ShellRegistration | null>(null)

  // Every AOP node is a valid vantage: the console's own host, tunnel nodes
  // (operations leave that node) and direct nodes (shared case directory).
  const vantages = (nodes.data?.nodes ?? []).filter((n) => n.transport !== 'audit' && !n.local)

  async function submit() {
    if (busy || !url.trim()) return
    setBusy(true)
    setError('')
    try {
      const res = await api.registerShell({
        url: url.trim(),
        type,
        protocol,
        name: name.trim() || undefined,
        node_id: nodeID && nodeID !== '__local__' ? nodeID : undefined,
        note: note.trim() || undefined,
        ...(adopt
          ? protocol === 'v2'
            ? { key: key.trim(), carrier: carrier.trim() }
            : { password }
          : {}),
      })
      setCreated(res)
      if (!res.payload) onCreated(res)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (created?.payload) {
    return (
      <Panel title="部署文件" extra={<Badge size="sm" variant="warning">只显示这一次</Badge>} bodyClassName="space-y-3">
        <p className="text-[12px] leading-relaxed text-ink-2">
          把下面的内容投放为 <span className="font-mono">{created.payload.filename}</span>，然后回到这个壳点「存活检查」。密钥已写进文件并封存在台账。
        </p>
        <Textarea readOnly value={created.payload.content} aria-label="部署文件内容" className="min-h-[220px] font-mono text-[11px]" />
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() => void navigator.clipboard.writeText(created.payload?.content ?? '').catch(() => undefined)}
          >
            复制内容
          </Button>
          <Button size="sm" onClick={() => onCreated(created)}>
            完成
          </Button>
        </div>
      </Panel>
    )
  }

  return (
    <Panel title="登记 webshell" extra={<Button size="xs" variant="ghost" onClick={onClose}>取消</Button>} bodyClassName="space-y-3">
      <Field label="地址" hint="http(s) 绝对地址，例如 http://10.9.9.2/shell.php">
        <Input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          aria-label="webshell 地址"
          placeholder="http://10.9.9.2/shell.php"
          spellCheck={false}
          className="font-mono text-[12px]"
          disabled={busy}
        />
      </Field>

      <div className="grid grid-cols-2 gap-2">
        <Field label="类型">
          <Select value={type} onValueChange={(v) => setType(v as typeof type)}>
            <SelectTrigger aria-label="webshell 类型" className="h-8 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="php">PHP</SelectItem>
              <SelectItem value="jsp">JSP</SelectItem>
              <SelectItem value="aspx">ASPX</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="协议">
          <Select value={protocol} onValueChange={(v) => setProtocol(v as typeof protocol)}>
            <SelectTrigger aria-label="webshell 协议" className="h-8 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="v2">v2 加密</SelectItem>
              <SelectItem value="legacy">legacy 明文</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>

      <Field
        label="执行节点"
        hint="本机 = 控制台所在主机；直连节点需与其共享项目目录；隧道节点的操作从它发出，目标看到的出口是它。"
      >
        <Select value={nodeID} onValueChange={setNodeID}>
          <SelectTrigger aria-label="执行节点" className="h-8 text-[12px]">
            <SelectValue placeholder="选择节点" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__local__">本机（默认）</SelectItem>
            {vantages.map((n) => (
              <SelectItem key={n.id} value={n.id}>
                {n.remote ? n.name || n.id : `直连 · ${n.name || n.id}`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      <label className="flex items-center gap-2 text-[12px] text-ink-2">
        <input type="checkbox" checked={adopt} onChange={(e) => setAdopt(e.target.checked)} disabled={busy} />
        目标上已有这个壳（接入连接材料，不生成文件）
      </label>

      {adopt && protocol === 'v2' && (
        <div className="space-y-2">
          <Field label="密钥" hint="部署文件里 {{KEY}} 的 base64 值">
            <Input value={key} onChange={(e) => setKey(e.target.value)} aria-label="v2 密钥" spellCheck={false} className="font-mono text-[12px]" disabled={busy} />
          </Field>
          <Field label="通信载体" hint="部署文件里的随机 header（X-…）或表单字段（jw_…）">
            <Input value={carrier} onChange={(e) => setCarrier(e.target.value)} aria-label="通信载体" spellCheck={false} className="font-mono text-[12px]" disabled={busy} />
          </Field>
        </div>
      )}
      {adopt && protocol === 'legacy' && (
        <Field label="连接口令">
          <Input value={password} onChange={(e) => setPassword(e.target.value)} aria-label="连接口令" type="password" className="font-mono text-[12px]" disabled={busy} />
        </Field>
      )}

      <div className="grid grid-cols-2 gap-2">
        <Field label="名称（可选）">
          <Input value={name} onChange={(e) => setName(e.target.value)} aria-label="名称" disabled={busy} />
        </Field>
        <Field label="备注（可选）">
          <Input value={note} onChange={(e) => setNote(e.target.value)} aria-label="备注" disabled={busy} />
        </Field>
      </div>

      {error && (
        <p className="text-[12px] text-destructive" role="alert">
          {error}
        </p>
      )}

      <Button size="sm" disabled={busy || !url.trim()} onClick={() => void submit()}>
        {adopt ? '接入' : '生成并登记'}
      </Button>
    </Panel>
  )
}
