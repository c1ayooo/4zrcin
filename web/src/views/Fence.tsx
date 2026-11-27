// The instance fence page (spec #1 工单3/#9): two deny-lists — commands that
// never execute and domains that are never reached — plus the cross-project
// audit of what they actually stopped.
//
// The lists are the operator's own red lines, not project state: one save
// rides the next turn on every node. Switches are honest states — off keeps
// every row and judges nothing — and enabling a list that holds rows is the
// one action that gets a confirmation.

import { useEffect, useMemo, useState } from 'react'
import { Plus, ShieldCheck, Trash2 } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  EmptyState,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SkeletonList,
  Spinner,
  Switch,
  useConfirm,
} from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { api } from '@/lib/api'
import {
  COMMAND_ACTION_LABELS,
  commandProblem,
  domainProblem,
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
  withAllowDomainAdded,
  withAllowDomainRemoved,
} from '@/lib/global-fence'
import { fmtTime } from '@/lib/format'
import { useLoad } from '@/lib/hooks'
import type { FenceHit, GlobalFence, GlobalFenceCommand } from '@/lib/types'

export function Fence() {
  const load = useLoad(api.globalFence, [])
  const [draft, setDraft] = useState<GlobalFence | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [receipt, setReceipt] = useState('')
  const [auditVersion, setAuditVersion] = useState(0)
  const confirm = useConfirm()

  useEffect(() => {
    if (load.data && !draft) setDraft(normalizeClientFence(load.data.fence))
  }, [load.data, draft])

  const summary = useMemo(() => (draft ? fenceSummary(draft) : null), [draft])

  async function commit(next: GlobalFence, note: string): Promise<boolean> {
    if (busy) return false
    setBusy(true)
    setError('')
    setReceipt('')
    try {
      const saved = await api.saveGlobalFence(forSave(next))
      setDraft(normalizeClientFence(saved.fence))
      setReceipt(note)
      setAuditVersion((n) => n + 1)
      return true
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return false
    } finally {
      setBusy(false)
    }
  }

  // The one-click preset load: the server appends the rows this fence does
  // not carry yet (idempotent), so the button is safe to press twice. The
  // confirm names the three categories and the review semantics — every
  // preset row asks the operator before the command runs.
  async function loadPreset() {
    if (busy || !draft) return
    const ok = await confirm({
      title: '载入命令预设？',
      description:
        '载入下载（wget/git clone/包安装/镜像拉取/curl 写文件）、删除（rm -r、find -delete、git clean、SQL DROP）、上传（scp/rsync/curl 上传）三类预设，全部走审批：命令执行前先问你。已存在的相同模式不会重复添加；载入后每行都可以单独修改或删除。',
      confirmLabel: '载入预设',
    })
    if (!ok) return
    setBusy(true)
    setError('')
    setReceipt('')
    try {
      const out = await api.fencePreset()
      setDraft(normalizeClientFence(out.fence))
      setReceipt(out.added > 0 ? `已载入 ${out.added} 条命令预设，下一轮起生效；每行可单独修改或删除。` : '预设已在清单里，没有新增行。')
      setAuditVersion((n) => n + 1)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function toggleCommands(next: boolean) {
    if (!draft) return
    if (needsEnableConfirm(draft.commands, next)) {
      const ok = await confirm({
        title: '启用命令禁令？',
        description: `清单里有 ${draft.commands.length} 条模式。启用后，下一轮起每个节点都会先判定命令：拒绝项直接不执行，待审项会挂起等待人工确认。关闭开关只停用判定，不删除清单。`,
        confirmLabel: '启用',
        destructive: true,
      })
      if (!ok) return
    }
    await commit({ ...draft, enabled_commands: next }, next ? '命令禁令已启用，下一轮起生效。' : '命令禁令已停用，清单保留。')
  }

  async function toggleDomains(next: boolean) {
    if (!draft) return
    if (needsEnableConfirm(draft.domains, next)) {
      const ok = await confirm({
        title: '启用域名禁令？',
        description: `清单里有 ${draft.domains.length} 条域名/网段。启用后，下一轮起任何项目对这些目标的请求都会在任何节点被拒绝，包括已派发的意图。关闭开关只停用判定，不删除清单。`,
        confirmLabel: '启用',
        destructive: true,
      })
      if (!ok) return
    }
    await commit({ ...draft, enabled_domains: next }, next ? '域名禁令已启用，下一轮起生效。' : '域名禁令已停用，清单保留。')
  }

  return (
    <Page
      title="围栏"
      subtitle="实例级红线：先于项目规则判定，每个节点、每一轮都生效"
      actions={
        summary ? (
          <span className="flex flex-wrap items-center gap-2 text-[11px] text-ink-3">
            <Badge size="sm" variant={summary.commandsArmed ? 'destructive' : 'muted'}>
              命令 {summary.commandsArmed ? '生效中' : '未生效'} · {summary.commands}
            </Badge>
            <Badge size="sm" variant={summary.domainsArmed ? 'destructive' : 'muted'}>
              域名 {summary.domainsArmed ? '生效中' : '未生效'} · {summary.domains}
            </Badge>
          </span>
        ) : undefined
      }
    >
      <div className="space-y-3">
        {load.error && (
          <Callout tone="destructive">
            读围栏失败：{load.error}
            <Button size="xs" variant="outline" className="ml-2" onClick={load.reload}>
              重试
            </Button>
          </Callout>
        )}
        {load.loading && !load.data && <SkeletonList count={4} />}
        {error && <Callout tone="destructive">{error}</Callout>}
        {receipt && <p className="text-[11.5px] leading-5 text-ink-2">{receipt}</p>}

        {draft && (
          <>
            <div className="grid gap-3 lg:grid-cols-2">
              <CommandList
                fence={draft}
                busy={busy}
                onToggle={(next) => void toggleCommands(next)}
                onPreset={() => void loadPreset()}
                onAdd={(row) =>
                  commit(withCommandAdded(draft, row), `已添加命令禁令：${row.pattern}`)
                }
                onUpdate={(id, patch) => commit(withCommandUpdated(draft, id, patch), '命令禁令已更新。')}
                onRemove={(id) => commit(withCommandRemoved(draft, id), '命令禁令已删除。')}
              />
              <DomainList
                fence={draft}
                busy={busy}
                onToggle={(next) => void toggleDomains(next)}
                onAdd={(host) => commit(withDomainAdded(draft, { id: '', host }), `已添加域名禁令：${host}`)}
                onUpdate={(id, host) => commit(withDomainUpdated(draft, id, { host }), '域名禁令已更新。')}
                onRemove={(id) => commit(withDomainRemoved(draft, id), '域名禁令已删除。')}
              />
            </div>
            <AllowDomainList
              fence={draft}
              busy={busy}
              onToggle={(next) => commit({ ...draft, enabled_allow_domains: next }, next ? '默认放行已启用，下一轮起生效。' : '默认放行已停用，清单保留。')}
              onAdd={(host) => commit(withAllowDomainAdded(draft, { id: '', host }), `已添加放行域名：${host}`)}
              onRemove={(id) => commit(withAllowDomainRemoved(draft, id), '放行域名已移除。')}
              onPreset={async () => {
                if (busy) return
                const ok = await confirm({
                  title: '载入基础设施放行预设？',
                  description: '载入 DNS/DoH 解析入口与被动情报源（crt.sh、certspotter 等）的默认放行：这些基础设施执行者始终可以访问，已存在的相同域名不重复添加。载入后每行可单独删除。',
                  confirmLabel: '载入预设',
                })
                if (!ok) return
                setBusy(true)
                setError('')
                setReceipt('')
                try {
                  const out = await api.fencePreset('allow_domains')
                  setDraft(normalizeClientFence(out.fence))
                  setReceipt(out.added > 0 ? `已载入 ${out.added} 条放行域名，下一轮起生效；每行可单独删除。` : '放行域名已在清单里，没有新增行。')
                  setAuditVersion((n) => n + 1)
                } catch (e) {
                  setError(e instanceof Error ? e.message : String(e))
                } finally {
                  setBusy(false)
                }
              }}
            />
            <AuditPanel version={auditVersion} />
            <NodeCoverage />
          </>
        )}
      </div>
    </Page>
  )
}

function AllowDomainList({
  fence,
  busy,
  onToggle,
  onAdd,
  onRemove,
  onPreset,
}: {
  fence: GlobalFence
  busy: boolean
  onToggle: (next: boolean) => void
  onAdd: (host: string) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
  onPreset: () => Promise<void>
}) {
  const [host, setHost] = useState('')
  const rows = fence.allow_domains ?? []
  return (
    <Panel
      title="默认放行域名"
      extra={
        <span className="flex items-center gap-2 text-[11px] text-ink-3">
          <Button size="xs" variant="outline" disabled={busy} onClick={() => void onPreset()}>
            载入基础设施预设
          </Button>
          {fence.enabled_allow_domains ? '判定中' : '已停用'}
          <Switch checked={fence.enabled_allow_domains} disabled={busy} onCheckedChange={onToggle} aria-label="默认放行开关" />
        </span>
      }
      bodyClassName="p-0"
    >
      {rows.length === 0 ? (
        <p className="px-3 py-4 text-[12px] text-ink-3">
          还没有放行域名。DNS/DoH 入口与被动情报源用「载入基础设施预设」一键加入；命中放行行的请求转发并记录，不进待审。
        </p>
      ) : (
        <ul role="list" className="divide-y divide-hairline">
          {rows.map((row) => (
            <li key={row.id} className="flex items-center gap-2 px-3 py-2 text-[12px]">
              <span className="min-w-0 truncate font-mono text-ink">{row.host}</span>
              {row.desc && <span className="min-w-0 truncate text-[11px] text-ink-3">{row.desc}</span>}
              <Button size="xs" variant="ghost" className="ml-auto shrink-0 text-destructive" disabled={busy} onClick={() => void onRemove(row.id)}>
                移除
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

function CommandList({
  fence,
  busy,
  onToggle,
  onPreset,
  onAdd,
  onUpdate,
  onRemove,
}: {
  fence: GlobalFence
  busy: boolean
  onToggle: (next: boolean) => void
  onPreset: () => void
  onAdd: (row: Omit<GlobalFenceCommand, 'id'>) => Promise<boolean>
  onUpdate: (id: string, patch: Partial<GlobalFenceCommand>) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  return (
    <Panel
      title="不可执行命令"
      extra={
        <span className="flex items-center gap-2 text-[11px] text-ink-3">
          <Button size="xs" variant="outline" disabled={busy} onClick={() => void onPreset()}>
            载入预设
          </Button>
          {fence.enabled_commands ? '判定中' : '已停用'}
          <Switch checked={fence.enabled_commands} disabled={busy} onCheckedChange={onToggle} aria-label="命令禁令开关" />
        </span>
      }
      bodyClassName="p-0"
    >
      {fence.commands.length === 0 ? (
        <p className="px-3 py-4 text-[12px] text-ink-3">还没有命令禁令。模式按 Go 正则匹配工具命令原文。</p>
      ) : (
        <ul role="list" className="divide-y divide-hairline">
          {fence.commands.map((row) => (
            <CommandRow key={row.id} row={row} busy={busy} onUpdate={onUpdate} onRemove={onRemove} />
          ))}
        </ul>
      )}
      <AddCommand busy={busy} onAdd={onAdd} />
    </Panel>
  )
}

function CommandRow({
  row,
  busy,
  onUpdate,
  onRemove,
}: {
  row: GlobalFenceCommand
  busy: boolean
  onUpdate: (id: string, patch: Partial<GlobalFenceCommand>) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  const [editing, setEditing] = useState(false)
  const [pattern, setPattern] = useState(row.pattern)
  const [desc, setDesc] = useState(row.desc ?? '')
  const [action, setAction] = useState(row.action)
  const problem = editing ? commandProblem(pattern, desc) : undefined

  async function save() {
    if (problem) return
    if (await onUpdate(row.id, { pattern: pattern.trim(), desc: desc.trim(), action })) setEditing(false)
  }

  return (
    <li className="px-3 py-2">
      {editing ? (
        <div className="space-y-1.5">
          <Input value={pattern} onChange={(e) => setPattern(e.target.value)} aria-label="命令匹配模式" className="h-7 font-mono text-[11.5px]" spellCheck={false} />
          <div className="flex gap-1.5">
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} aria-label="命令禁令说明" placeholder="说明（可选）" className="h-7 text-[11.5px]" />
            <Select value={action} onValueChange={(value) => setAction(value as GlobalFenceCommand['action'])}>
              <SelectTrigger className="h-7 w-[92px] text-[11.5px]" aria-label="命令禁令动作">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="deny">拒绝</SelectItem>
                <SelectItem value="review">待审</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {problem && <p className="text-[11px] text-destructive">{problem}</p>}
          <div className="flex gap-1.5">
            <Button size="xs" onClick={() => void save()} disabled={busy || Boolean(problem)}>
              保存
            </Button>
            <Button size="xs" variant="ghost" onClick={() => { setEditing(false); setPattern(row.pattern); setDesc(row.desc ?? ''); setAction(row.action) }} disabled={busy}>
              取消
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <span className="zrcin-break min-w-0 flex-1 font-mono text-[11.5px] text-ink">{row.pattern}</span>
          <Badge size="sm" variant={row.action === 'deny' ? 'destructive' : 'warning'}>
            {COMMAND_ACTION_LABELS[row.action] ?? row.action}
          </Badge>
          {row.desc && <span className="zrcin-break w-full text-[10.5px] text-ink-3">{row.desc}</span>}
          <Button size="xs" variant="ghost" className="px-1.5" onClick={() => setEditing(true)} disabled={busy}>
            编辑
          </Button>
          <Button size="xs" variant="ghost" className="px-1.5 text-destructive" onClick={() => void onRemove(row.id)} disabled={busy} aria-label={`删除 ${row.pattern}`}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}
    </li>
  )
}

function AddCommand({
  busy,
  onAdd,
}: {
  busy: boolean
  onAdd: (row: Omit<GlobalFenceCommand, 'id'>) => Promise<boolean>
}) {
  const [open, setOpen] = useState(false)
  const [pattern, setPattern] = useState('')
  const [desc, setDesc] = useState('')
  const [action, setAction] = useState<GlobalFenceCommand['action']>('deny')
  const problem = open ? commandProblem(pattern, desc) : undefined

  async function add() {
    if (problem) return
    if (await onAdd({ pattern: pattern.trim(), desc: desc.trim(), action })) {
      setPattern('')
      setDesc('')
      setAction('deny')
      setOpen(false)
    }
  }

  if (!open) {
    return (
      <div className="border-t border-hairline px-3 py-2">
        <Button size="xs" variant="ghost" className="gap-1 px-1.5" onClick={() => setOpen(true)} disabled={busy}>
          <Plus className="h-3.5 w-3.5" /> 添加模式
        </Button>
      </div>
    )
  }
  return (
    <div className="space-y-1.5 border-t border-hairline px-3 py-2">
      <Input value={pattern} onChange={(e) => setPattern(e.target.value)} placeholder="例如：\\brm\\s+-rf\\b" aria-label="新的命令匹配模式" className="h-7 font-mono text-[11.5px]" spellCheck={false} autoFocus />
      <div className="flex gap-1.5">
        <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="说明（可选）" aria-label="新的命令禁令说明" className="h-7 text-[11.5px]" />
        <Select value={action} onValueChange={(value) => setAction(value as GlobalFenceCommand['action'])}>
          <SelectTrigger className="h-7 w-[92px] text-[11.5px]" aria-label="新的命令禁令动作">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="deny">拒绝</SelectItem>
            <SelectItem value="review">待审</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {problem && <p className="text-[11px] text-destructive">{problem}</p>}
      <div className="flex gap-1.5">
        <Button size="xs" onClick={() => void add()} disabled={busy || Boolean(problem)}>
          {busy && <Spinner size="sm" className="mr-1" />}添加
        </Button>
        <Button size="xs" variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
          取消
        </Button>
      </div>
    </div>
  )
}

function DomainList({
  fence,
  busy,
  onToggle,
  onAdd,
  onUpdate,
  onRemove,
}: {
  fence: GlobalFence
  busy: boolean
  onToggle: (next: boolean) => void
  onAdd: (host: string) => Promise<boolean>
  onUpdate: (id: string, host: string) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  const [host, setHost] = useState('')
  const problem = host ? domainProblem(host) : undefined

  async function add() {
    if (problem || !host.trim()) return
    if (await onAdd(host.trim())) setHost('')
  }

  return (
    <Panel
      title="不可访问域名"
      extra={
        <span className="flex items-center gap-2 text-[11px] text-ink-3">
          {fence.enabled_domains ? '判定中' : '已停用'}
          <Switch checked={fence.enabled_domains} disabled={busy} onCheckedChange={onToggle} aria-label="域名禁令开关" />
        </span>
      }
      bodyClassName="p-0"
    >
      {fence.domains.length === 0 ? (
        <p className="px-3 py-4 text-[12px] text-ink-3">
          还没有域名禁令。一行覆盖裸域及其全部子域；网段写 CIDR（10.0.0.0/8）。
        </p>
      ) : (
        <ul role="list" className="divide-y divide-hairline">
          {fence.domains.map((row) => (
            <DomainRow key={row.id} row={row} busy={busy} onUpdate={onUpdate} onRemove={onRemove} />
          ))}
        </ul>
      )}
      <div className="space-y-1.5 border-t border-hairline px-3 py-2">
        <div className="flex gap-1.5">
          <Input
            value={host}
            onChange={(e) => setHost(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                void add()
              }
            }}
            placeholder="gov.cn 或 10.0.0.0/8"
            aria-label="新的域名禁令"
            className="h-7 font-mono text-[11.5px]"
            spellCheck={false}
          />
          <Button size="xs" variant="outline" onClick={() => void add()} disabled={busy || !host.trim() || Boolean(problem)}>
            添加
          </Button>
        </div>
        {problem && <p className="text-[11px] text-destructive">{problem}</p>}
      </div>
    </Panel>
  )
}

function DomainRow({
  row,
  busy,
  onUpdate,
  onRemove,
}: {
  row: { id: string; host: string }
  busy: boolean
  onUpdate: (id: string, host: string) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  const [editing, setEditing] = useState(false)
  const [host, setHost] = useState(row.host)
  const problem = editing ? domainProblem(host) : undefined

  async function save() {
    if (problem) return
    if (await onUpdate(row.id, host.trim())) setEditing(false)
  }

  return (
    <li className="px-3 py-2">
      {editing ? (
        <div className="space-y-1.5">
          <Input value={host} onChange={(e) => setHost(e.target.value)} aria-label="域名禁令" className="h-7 font-mono text-[11.5px]" spellCheck={false} autoFocus />
          {problem && <p className="text-[11px] text-destructive">{problem}</p>}
          <div className="flex gap-1.5">
            <Button size="xs" onClick={() => void save()} disabled={busy || Boolean(problem)}>保存</Button>
            <Button size="xs" variant="ghost" onClick={() => { setEditing(false); setHost(row.host) }} disabled={busy}>取消</Button>
          </div>
        </div>
      ) : (
        <div className="flex items-center gap-2">
          <span className="zrcin-break min-w-0 flex-1 font-mono text-[11.5px] text-ink">{row.host}</span>
          <Button size="xs" variant="ghost" className="px-1.5" onClick={() => setEditing(true)} disabled={busy}>
            编辑
          </Button>
          <Button size="xs" variant="ghost" className="px-1.5 text-destructive" onClick={() => void onRemove(row.id)} disabled={busy} aria-label={`删除 ${row.host}`}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}
    </li>
  )
}

function AuditPanel({ version }: { version: number }) {
  const [source, setSource] = useState('')
  const [action, setAction] = useState('')
  const hits = useLoad(
    () => api.fenceHits({ source: source || undefined, action: action || undefined, limit: 200 }),
    [source, action, version],
  )
  const counts = useMemo(() => hitCounts(hits.data?.hits ?? []), [hits.data])

  return (
    <Panel
      title="命中审计"
      extra={
        <span className="flex items-center gap-1.5">
          <FilterChips
            label="来源"
            value={source}
            onChange={setSource}
            options={[
              { id: '', label: '全部' },
              { id: 'global', label: '全局' },
              { id: 'rule', label: '项目规则' },
            ]}
          />
          <FilterChips
            label="动作"
            value={action}
            onChange={setAction}
            options={[
              { id: '', label: '全部' },
              { id: 'block', label: '拒绝' },
              { id: 'review', label: '待审' },
            ]}
          />
        </span>
      }
      bodyClassName="p-0"
    >
      {hits.error && (
        <div className="px-3 py-3">
          <Callout tone="destructive">读审计失败：{hits.error}</Callout>
        </div>
      )}
      {hits.loading && !hits.data && (
        <div className="p-3">
          <SkeletonList count={4} />
        </div>
      )}
      {hits.data && (hits.data.hits ?? []).length === 0 && (
        <EmptyState compact icon={ShieldCheck} title="还没有命中记录" description="围栏触发后，这里会按时间列出所有项目里的命中；改规则不会删除历史。" />
      )}
      {hits.data && (hits.data.hits ?? []).length > 0 && (
        <>
          <div className="zrcin-scroll max-h-[min(520px,58vh)] overflow-auto overscroll-contain [scrollbar-width:thin]">
            <table className="w-full min-w-[720px] border-separate border-spacing-0 text-[11.5px]">
              <thead className="sticky top-0 z-10 bg-surface-2 text-left text-[10.5px] font-medium text-ink-3">
                <tr>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">时间</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">项目</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">来源</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">类型</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">规则</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">命中内容</th>
                  <th scope="col" className="border-b border-hairline px-3 py-1.5 font-medium">动作</th>
                </tr>
              </thead>
              <tbody>
                {(hits.data.hits ?? []).map((hit, index) => (
                  <AuditRow key={`${hit.project_id}-${hit.run_id}-${hit.rule_id}-${hit.at}-${index}`} hit={hit} />
                ))}
              </tbody>
            </table>
          </div>
          <p className="border-t border-hairline px-3 py-1.5 text-[10.5px] text-ink-3">
            共 {hits.data.total} 条{hits.data.total > hits.data.hits.length ? `，显示最近 ${hits.data.hits.length} 条` : ''}
            {counts.global + counts.rule > 0 ? ` · 全局 ${counts.global} · 项目规则 ${counts.rule}` : ''}
          </p>
        </>
      )}
    </Panel>
  )
}

function AuditRow({ hit }: { hit: FenceHit }) {
  const review = hit.action === 'review'
  return (
    <tr className="border-b border-hairline last:border-0">
      <td className="whitespace-nowrap px-3 py-1.5 font-mono text-[10.5px] text-ink-3">{fmtTime(hit.at)}</td>
      <td className="zrcin-break px-3 py-1.5 text-[11px] text-ink-2">{hit.project || hit.project_id}</td>
      <td className="px-3 py-1.5">
        <Badge size="sm" variant={hit.source === 'global' ? 'primary' : 'secondary'}>
          {hitSourceLabel(hit.source)}
        </Badge>
      </td>
      <td className="px-3 py-1.5 text-[11px] text-ink-2">{hitKindLabel(hit.kind)}</td>
      <td className="zrcin-break max-w-[220px] px-3 py-1.5 font-mono text-[10.5px] text-ink-3" title={hitRuleText(hit)}>{hitRuleText(hit)}</td>
      <td className="zrcin-break max-w-[320px] px-3 py-1.5 font-mono text-[10.5px] text-ink-2" title={hit.content}>{hit.content}</td>
      <td className="px-3 py-1.5">
        <Badge size="sm" variant={review ? 'warning' : 'destructive'}>{hitActionLabel(hit)}</Badge>
      </td>
    </tr>
  )
}

function FilterChips({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (next: string) => void
  options: { id: string; label: string }[]
}) {
  return (
    <span role="group" aria-label={`按${label}筛选`} className="flex items-center gap-1">
      {options.map((option) => (
        <Button
          key={option.id || 'all'}
          size="xs"
          variant={value === option.id ? 'default' : 'ghost'}
          className="h-6 px-1.5 text-[10.5px]"
          aria-pressed={value === option.id}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </Button>
      ))}
    </span>
  )
}

/** The honest coverage reading (工单 #10's notes): the control plane can name
 *  which nodes it drives, but it cannot verify a remote build's fork baseline
 *  from the handshake. A node that predates patch 31 silently ignores the
 *  extension, so "not confirmed" must never render as "covered". */
function NodeCoverage() {
  const nodes = useLoad(api.remoteNodes, [])
  const remote = nodes.data?.nodes ?? []
  return (
    <Panel title="节点覆盖" bodyClassName="px-3 py-2.5">
      <p className="text-[11.5px] leading-5 text-ink-2">
        本地执行宿主随控制面构建，本版本包含全局围栏扩展；远端节点只有在部署同一 fork 基线（补丁 31 及之后）时才会判定。
        控制面无法从握手确认远端构建版本——下面标「无法确认」的节点按可能未覆盖对待，不当作红线已生效。
      </p>
      {nodes.error ? (
        <p className="mt-1.5 text-[11px] text-ink-3">节点接入未开启，无法列出远端节点。</p>
      ) : remote.length === 0 ? (
        <p className="mt-1.5 text-[11px] text-ink-3">没有通过接入通道加入的远端节点。</p>
      ) : (
        <ul role="list" className="mt-1.5 flex flex-wrap gap-1.5">
          {remote.map((node) => (
            <li key={node.id} className="zrcin-inset flex items-center gap-1.5 rounded-md px-2 py-1 text-[11px]">
              <span className="font-mono">{node.name || node.id}</span>
              <Badge size="sm" variant={node.online ? 'warning' : 'muted'}>
                {node.online ? '无法确认' : '离线'}
              </Badge>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}
