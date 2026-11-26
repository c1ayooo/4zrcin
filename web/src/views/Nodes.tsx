// 执行环境 · 节点 (docs/14 §9): one page for the whole node lifecycle.
//
// Left: the node list, grouped by management boundary (remote / waiting for
// a callback / direct / local). Right: the deploy rail (command deploy, the
// direct join command, the build recipes) or, while a terminal is open, the
// terminal pane. Everything deployment-related lives on this page.
import { Suspense, lazy, useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Copy, RefreshCw, SquareTerminal } from 'lucide-react'
import { Badge, Button, Callout, EmptyState, SkeletonList, StatusDot } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { Page, Panel } from '@/components/Page'
import { NodeCards, type NodeCardsData } from '@/components/NodeCards'
import { NodeDeployRail, type DeployMode } from '@/components/NodeDeployRail'
import { RowMenu } from '@/components/NodeRowMenu'
import { api } from '@/lib/api'
import { navigate } from '@/lib/navigation'
import { useConsoleLocation } from '@/lib/hooks'
import { usePoll } from '@/lib/hooks'
import { hostPresentation } from '@/lib/nodes'
import {
  activeDeployJobs,
  deployLabel,
  deployRow,
  deployTone,
  healthLabel,
  healthTone,
  nodeCardData,
  enrollmentRemaining,
  formatBytes,
  formatSince,
  groupNodes,
  pendingEnrollments,
  recipeLabel,
  remoteSessions,
  remoteStatus,
  sessionOf,
} from '@/lib/remote-nodes'
import type { DeployJob, EnrollmentView, NodeInfo, SshDeployRequest } from '@/lib/types'

// Opening this page must not download xterm: the terminal pane is the only
// consumer of that ~330 KB chunk.
const NodeTerminal = lazy(() =>
  import('@/components/NodeTerminal').then((m) => ({ default: m.NodeTerminal })),
)

const STATUS_TONE: Record<string, string> = {
  online: 'text-success',
  reconnecting: 'text-warning',
  destroying: 'text-destructive',
  destroyed: 'text-ink-3',
  offline: 'text-ink-3',
  unknown: 'text-ink-3',
}

const STATUS_DOT: Record<string, 'online' | 'warning' | 'pending' | 'idle'> = {
  online: 'online',
  reconnecting: 'warning',
  destroying: 'pending',
  destroyed: 'idle',
  offline: 'idle',
  unknown: 'idle',
}

function CopyCommandButton({ text, disabled }: { text: string; disabled?: boolean }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      size="xs"
      variant="ghost"
      disabled={disabled}
      title={disabled ? '命令只在生成时显示一次，重新生成新的接入记录即可' : '复制接入命令'}
      aria-label="复制接入命令"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text)
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1200)
        } catch {
          /* clipboard is best effort; the command stays visible in the rail */
        }
      }}
    >
      {copied ? <Check className="h-3.5 w-3.5" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
      {copied ? '已复制' : '复制命令'}
    </Button>
  )
}

export function Nodes() {
  const list = usePoll(api.nodes, 4000)
  const remote = usePoll(api.remoteNodes, 4000)
  const enrollments = usePoll(api.enrollments, 5000)

  const [terminal, setTerminal] = useState<string | null>(null)
  const [createdCommands, setCreatedCommands] = useState<Record<string, string>>({})
  // The SSH deployment job is polled here, not in the rail: the rail stays a
  // pure view of whatever the page hands it.
  // Deployments are platform objects, not page state: the page polls the
  // list, follows the running one, and restores it after a reload or a tab
  // switch (docs/14 §7.4). `deployParam` is the ?deploy= deep link.
  const [deployJob, setDeployJob] = useState<DeployJob | null>(null)
  const [railMode, setRailMode] = useState<DeployMode | null>(null)
  const [deployTick, setDeployTick] = useState(0)
  const route = useConsoleLocation()
  const deployParam = route.searchParams.get('deploy')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const nodes = list.data?.nodes ?? []
  const groups = useMemo(() => groupNodes(nodes), [nodes])
  // Deployments are watched like nodes: one row per job the operator still
  // needs to see (running first, then the ones that ended badly).
  const deploys = usePoll(api.nodeDeploys, 1000)
  const deployRows = useMemo(() => activeDeployJobs(deploys.data?.jobs), [deploys.data])
  const ownedEnrollmentIds = useMemo(
    () => new Set((deploys.data?.jobs ?? []).map((j) => j.enrollment_id)),
    [deploys.data],
  )
  const deployOwned = useCallback((id: string) => ownedEnrollmentIds.has(id), [ownedEnrollmentIds])
  const sessions = useMemo(() => remoteSessions(remote.data?.nodes), [remote.data])
  const pending = pendingEnrollments(enrollments.data?.enrollments, [...ownedEnrollmentIds])

  // The ingress is one switch: both instance endpoints answer 404 together.
  const enabled = Boolean(remote.data?.ingress?.enabled)
  const dist = enrollments.data?.dist ?? remote.data?.dist

  // Focusing a deployment tracks it AND forces the rail onto the SSH view: the
  // job's progress renders there, so "查看" must make it visible, not just
  // update hidden state.
  const onDeployFocus = (id: string) => {
    void (async () => {
      try {
        setDeployJob(await api.nodeDeploy(id))
        setRailMode('ssh')
        const url = new URL(window.location.href)
        url.searchParams.set('deploy', id)
        navigate(url.pathname + url.search + url.hash, true)
      } catch {
        // A gone id (cleared from the file) ends the tracking quietly.
        setDeployJob(null)
      }
    })()
  }

  // Project placement: the menu reads the current binding so one click maps to
  // one api call (no hidden local copy of the allowlist).
  const boundIds = nodes.filter((n) => n.bound).map((n) => n.id)
  const projectDefault = list.data?.project_default ?? ''
  const onChanged = (text: string) => {
    setNotice(text)
    refresh()
  }

  const refresh = useCallback(() => {
    list.reload()
    remote.reload()
    enrollments.reload()
  }, [list, remote, enrollments])

  useEffect(() => {
    // The tracked id is whichever job the operator is watching: the running
    // one, the ?deploy= deep link, or the one this page just started.
    const id = deployJob?.state === 'running' ? deployJob.id : (deployParam ?? deployJob?.id)
    if (!id) return
    const timer = window.setTimeout(async () => {
      try {
        const fresh = await api.nodeDeploy(id)
        setDeployJob((prev) => (prev && prev.id === fresh.id ? fresh : prev?.state === 'running' ? prev : fresh))
      } catch {
        // A gone id (cleared from the file) just ends the tracking.
        setDeployJob((prev) => (prev?.id === id ? null : prev))
      } finally {
        setDeployTick((n) => n + 1)
      }
    }, 1000)
    return () => window.clearTimeout(timer)
  }, [deployJob, deployTick, deployParam])

  // Restore on mount and follow the list once a tracked job ends: the page
  // fetches the registry, prefers the running job, then the deep link.
  const refreshDeploys = useCallback(async () => {
    try {
      const list = await api.nodeDeploys()
      const running = list.jobs.find((j) => j.state === 'running')
      const wanted = deployParam ? list.jobs.find((j) => j.id === deployParam) : undefined
      setDeployJob(running ?? wanted ?? list.jobs[0] ?? null)
    } catch {
      /* the registry read failing keeps whatever the page had */
    }
  }, [deployParam])
  useEffect(() => {
    if (!deployJob) void refreshDeploys()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function revoke(enrollment: EnrollmentView) {
    setSaving(true)
    setError('')
    try {
      await api.revokeEnrollment(enrollment.id)
      setCreatedCommands(({ [enrollment.id]: _removed, ...rest }) => rest)
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Page
      title="节点"
      scroll={false}
      className="@container"
      actions={
        <Button variant="outline" size="sm" onClick={refresh} disabled={list.loading || list.refreshing}>
          <RefreshCw className={cn('h-3.5 w-3.5', list.refreshing && 'motion-safe:animate-spin')} aria-hidden="true" />
          刷新状态
        </Button>
      }
    >
      <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto overscroll-contain py-1 @4xl:flex-row @4xl:overflow-hidden">
        <div className="min-w-0 flex-none space-y-4 @4xl:flex-1 @4xl:overflow-y-auto @4xl:pr-1">
          <p role="status" aria-live="polite" className="sr-only">
            执行节点共 {nodes.length} 个，其中远程 {groups.remote.length} 个、待回连 {pending.length} 条{notice ? `；${notice}` : ''}
          </p>
          {error && <Callout tone="destructive">{error}</Callout>}
          {list.data?.discovery_error && (
            <Callout tone="warning">节点发现暂不可用，已保留上次列表：{list.data.discovery_error}</Callout>
          )}

          {list.loading && (
            <Panel bodyClassName="p-4">
              <SkeletonList count={3} />
            </Panel>
          )}

          {!list.loading && nodes.length === 0 && (
            <Panel bodyClassName="p-0">
              <EmptyState
                icon={SquareTerminal}
                title="没有执行节点"
                description="右侧接入一台远程节点，或检查本机 Hub 与 scan。"
              />
            </Panel>
          )}

          {deployRows.length > 0 && (
            <Panel
              title="部署中"
              extra={<span className="zrcin-nums font-mono text-[11px] text-ink-3">{deployRows.length} 个</span>}
              bodyClassName="p-0"
            >
              <ul role="list" className="divide-y divide-hairline" aria-label="部署中的任务">
                {deployRows.map((job: DeployJob) => {
                  const row = deployRow(job, Date.now())
                  const isTracked = deployJob?.id === job.id
                  const tone = deployTone(job.state)
                  return (
                    <li key={job.id} className={cn('px-3 py-2.5 transition-colors', isTracked ? 'bg-primary/[0.06]' : 'hover:bg-surface-2')}>
                      <div className="flex items-start gap-2.5">
                        <StatusDot
                          status={
                            job.state === 'running'
                              ? 'running'
                              : tone === 'destructive'
                                ? 'error'
                                : tone === 'success'
                                  ? 'online'
                                  : tone === 'warning'
                                    ? 'warning'
                                    : 'idle'
                          }
                          className="mt-1.5"
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                            <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">{row.title}</span>
                            <span
                              className={cn(
                                'text-xs',
                                job.state === 'running' && 'text-ink-2',
                                job.state === 'succeeded' && 'text-success',
                                job.state === 'interrupted' && 'text-warning',
                                job.state === 'failed' && 'text-destructive',
                              )}
                            >
                              {row.status}
                            </span>
                          </div>
                          {row.detail && <p className="zrcin-break mt-1 font-mono text-[11px] leading-4 text-ink-3">{row.detail}</p>}
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          <Button size="xs" variant={isTracked ? 'soft' : 'outline'} onClick={() => onDeployFocus(job.id)}>
                            {isTracked ? '查看' : '详情'}
                          </Button>
                          {job.state !== 'running' && (
                            <Button size="xs" variant="ghost" onClick={() => void api.dismissNodeDeploy(job.id).then(() => deploys.reload())}>
                              收起
                            </Button>
                          )}
                        </div>
                      </div>
                    </li>
                  )
                })}
              </ul>
            </Panel>
          )}

          {groups.remote.length > 0 && (
            <NodeGroup
              title="远程节点"
              nodes={groups.remote}
              sessions={sessions}
              terminal={terminal}
              onTerminal={setTerminal}
              boundIds={boundIds}
              projectDefault={projectDefault}
              onChanged={onChanged}
            />
          )}

          {groups.direct.length > 0 && (
            <DirectGroup
              nodes={groups.direct}
              terminal={terminal}
              onTerminal={setTerminal}
              boundIds={boundIds}
              projectDefault={projectDefault}
              onChanged={onChanged}
            />
          )}
          {groups.local.length > 0 && (
            <LocalGroup
              nodes={groups.local}
              terminal={terminal}
              onTerminal={setTerminal}
              boundIds={boundIds}
              projectDefault={projectDefault}
              onChanged={onChanged}
            />
          )}
          {pending.length > 0 && (
            <Panel
              title="待回连"
              extra={<span className="zrcin-nums font-mono text-[11px] text-ink-3">{pending.length} 个</span>}
              bodyClassName="p-0"
            >
              <ul role="list" className="divide-y divide-hairline" aria-label="待回连的接入记录">
                {pending.map((e) => (
                  <li key={e.id} className="flex items-start gap-2.5 px-3 py-2.5">
                    <StatusDot status="pending" className="mt-1.5" />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                        <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">
                          {e.name || `待接入 · ${e.id.slice(0, 6)}`}
                        </span>
                        <span className="text-xs text-ink-3">{enrollmentRemaining(e.expires_at)}</span>
                        <span className="font-mono text-[11px] text-ink-3">
                          {recipeLabel(e.recipe)} · {deployLabel(e.deploy)}
                        </span>
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <CopyCommandButton text={createdCommands[e.id] ?? ''} disabled={!createdCommands[e.id]} />
                      <Button size="xs" variant="ghost" disabled={saving} onClick={() => void revoke(e)}>
                        撤销
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            </Panel>
          )}


        </div>

        <div
          className={cn(
            'min-w-0 flex-none pb-1',
            terminal ? 'flex h-[420px] flex-col gap-2 @4xl:h-auto @4xl:flex-[1.15]' : '@4xl:w-96 @4xl:max-h-full @4xl:overflow-y-auto @4xl:pr-1',
          )}
        >
          {terminal ? (
            <>
              <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl">
                <Suspense
                  fallback={
                    <div className="zrcin-panel flex h-full min-h-[200px] flex-col gap-3 rounded-xl p-4">
                      <span className="text-[12px] text-ink-2">终端内核加载中…</span>
                      <SkeletonList count={4} />
                    </div>
                  }
                >
                  <NodeTerminal node={terminal} onClose={() => setTerminal(null)} />
                </Suspense>
              </div>
              <p className="zrcin-break px-1 font-mono text-[11px] leading-4 text-ink-3">
                终端运行在所选 scan 节点；关闭面板不会停止节点。
              </p>
            </>
          ) : (
            <Panel title="部署" bodyClassName="p-3">
              {remote.error && !enrollments.data && !remote.data ? (
                <Callout tone="warning">无法读取接入状态：{remote.error}</Callout>
              ) : null}
              <NodeDeployRail
                enabled={enabled}
                relay={remote.data?.ingress?.relay}
                mode={railMode ?? (deployJob ? 'ssh' : enabled ? 'command' : 'direct')}
                onModeChange={setRailMode}
                dist={dist}
                enrollments={enrollments.data?.enrollments ?? []}
                onCreate={async (options) => {
                  const created = await api.createEnrollment({ ...options, deploy: 'command' })
                  setCreatedCommands((prev) => ({ ...prev, [created.id]: created.install_command }))
                  enrollments.reload()
                  return created
                }}
                onJoinCommand={async () => {
                  const join = await api.nodeJoin()
                  return `cyber-scan ${join.args.map((a) => `'${a.replace(/'/g, `'"'"'`)}'`).join(' ')}`
                }}
                deployJob={deployJob}
                onStartDeploy={async (request: SshDeployRequest) => {
                  setNotice('')
                  const started = await api.startNodeDeploy(request)
                  setDeployJob(started)
                  setRailMode('ssh')
                  // The job is a URL addressable object: ?deploy=<id> brings the
                  // page back to it after a reload or from another tab.
                  const url = new URL(window.location.href)
                  url.searchParams.set('deploy', started.id)
                  navigate(url.pathname + url.search + url.hash, true)
                  enrollments.reload()
                }}
                onResetDeploy={() => {
                  // A failed attempt leaves its invitation pending; revoke it so
                  // the waiting list stays honest. A record that joined in the
                  // meantime answers 409 — the node exists and destroy is then
                  // the operator's action.
                  if (deployJob?.state === 'failed') {
                    void api
                      .revokeEnrollment(deployJob.enrollment_id)
                      .catch(() => undefined)
                      .then(() => enrollments.reload())
                  }
                  setDeployJob(null)
                }}
                onDismissDeploy={() => {
                  if (!deployJob) return
                  void api.dismissNodeDeploy(deployJob.id).catch(() => undefined)
                  const url = new URL(window.location.href)
                  url.searchParams.delete('deploy')
                  navigate(url.pathname + url.search + url.hash, true)
                  setDeployJob(null)
                }}
              />
            </Panel>
          )}
        </div>
      </div>
    </Page>
  )
}

interface GroupProps {
  nodes: NodeInfo[]
  terminal: string | null
  onTerminal: (id: string | null) => void
  boundIds: string[]
  projectDefault: string
  onChanged: (notice: string) => void
}

/** GroupProps + the detail-card expansion (remote and local groups only). */
interface ExpandableGroupProps extends GroupProps {
}

function NodeGroup({
  title,
  nodes,
  sessions,
  terminal,
  onTerminal,
  boundIds,
  projectDefault,
  onChanged,
}: ExpandableGroupProps & { title: string; sessions: ReturnType<typeof remoteSessions> }) {
  return (
    <Panel
      title={title}
      extra={<span className="zrcin-nums font-mono text-[11px] text-ink-3">{nodes.length} 个</span>}
      bodyClassName="p-0"
    >
      <ul role="list" className="divide-y divide-hairline" aria-label={title}>
        {nodes.map((n) => {
          const status = remoteStatus(n.remote)
          const session = sessionOf(sessions, n)
          const where = [session?.hostname || n.name, osArch(session?.os, session?.arch)].filter(Boolean).join(' · ')
          const traffic = session?.tunnel?.up
            ? `↑${formatBytes(session.tunnel.bytes_in)} ↓${formatBytes(session.tunnel.bytes_out)}`
            : ''
          const since = formatSince(session?.tunnel?.since)
          const secondary = [where, traffic, since].filter(Boolean).join(' · ')
          const cards = nodeCardData(n, session)
          return (
            <li key={n.id} className={cn('px-3 py-2.5 transition-colors', terminal === n.id ? 'bg-primary/[0.09] shadow-[inset_2px_0_0_var(--primary)]' : 'hover:bg-surface-2')}>
              <div className="flex items-start gap-2.5">
                <StatusDot status={STATUS_DOT[status.kind]} className="mt-1.5" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                    <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">
                      {session?.name || n.name || n.id}
                    </span>
                    <span className={cn('text-xs', STATUS_TONE[status.kind])}>{status.label}</span>
                    <span className="font-mono text-[11px] text-ink-3">
                      {recipeLabel(n.remote?.recipe)} · {deployLabel(n.remote?.deploy) || '命令部署'}
                    </span>
                  </div>
                  {secondary && (
                    <p className="zrcin-break mt-1 font-mono text-[11px] leading-4 text-ink-3">{secondary}</p>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant={terminal === n.id ? 'soft' : 'outline'}
                    size="xs"
                    active={terminal === n.id}
                    disabled={!n.terminal}
                    aria-label={`打开 ${n.id} 的终端`}
                    onClick={() => onTerminal(terminal === n.id ? null : n.id)}
                  >
                    <SquareTerminal className="h-3 w-3" aria-hidden="true" />
                    终端
                  </Button>
                  <RowMenu
                    node={n}
                    title={session?.name || n.name || n.id}
                    boundIds={boundIds}
                    projectDefault={projectDefault}
                    onChanged={onChanged}
                  />
                </div>
              </div>
              <NodeCards
                data={{...cards, tone: healthTone(cards.health.state), label: healthLabel(cards.health.state)}}
                ariaLabel={`${session?.name || n.id} 的详情卡片`}
              />
            </li>
          )
        })}
      </ul>
    </Panel>
  )
}

function DirectGroup({ nodes, terminal, onTerminal, boundIds, projectDefault, onChanged }: GroupProps) {
  return (
    <Panel title="直连" extra={<span className="zrcin-nums font-mono text-[11px] text-ink-3">{nodes.length} 个</span>} bodyClassName="p-0">
      <ul role="list" className="divide-y divide-hairline" aria-label="直连节点">
        {nodes.map((n) => {
          const runtime = hostPresentation(
            n.host ?? (n.online !== undefined ? { online: n.online, ready: !!n.ready, last_error: n.config_error, pid: 0, restarts: 0, endpoint: n.endpoint ?? '', failed_ready: 0, unhealthy: false } : undefined),
          )
          const cards = nodeCardData(n, undefined)
          return (
            <li key={n.id} className="px-3 py-2.5 hover:bg-surface-2">
              <div className="flex items-start gap-2.5">
                <StatusDot status={STATUS_DOT[runtime.status === 'online' ? 'online' : runtime.status === 'warning' ? 'reconnecting' : 'offline']} className="mt-1.5" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                    <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">{n.name || n.id}</span>
                    <span className={cn('text-xs', runtime.status === 'online' ? 'text-success' : runtime.status === 'warning' ? 'text-warning' : 'text-ink-3')}>
                      {runtime.label}
                    </span>
                    {n.capabilities?.includes('scan-capture') && (
                      <Badge size="sm" variant="warning">
                        capture
                      </Badge>
                    )}
                    {n.project_default && <span className="text-[11px] text-ink-3">项目默认</span>}
                  </div>
                  {n.endpoint && <p className="zrcin-break mt-1 font-mono text-[11px] leading-4 text-ink-3">{n.endpoint}</p>}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant={terminal === n.id ? 'soft' : 'outline'}
                    size="xs"
                    active={terminal === n.id}
                    disabled={!n.terminal}
                    aria-label={`打开 ${n.id} 的终端`}
                    onClick={() => onTerminal(terminal === n.id ? null : n.id)}
                  >
                    <SquareTerminal className="h-3 w-3" aria-hidden="true" />
                    终端
                  </Button>
                  <RowMenu node={n} title={n.name || n.id} boundIds={boundIds} projectDefault={projectDefault} onChanged={onChanged} />
                </div>
              </div>
              <NodeCards
                data={{...cards, tone: healthTone(cards.health.state), label: healthLabel(cards.health.state)}}
                ariaLabel={`${n.name || n.id} 的详情卡片`}
              />
            </li>
          )
        })}
      </ul>
    </Panel>
  )
}

function LocalGroup({ nodes, terminal, onTerminal, boundIds, projectDefault, onChanged }: GroupProps) {
  return (
    <Panel title="本机" bodyClassName="p-0">
      <ul role="list" className="divide-y divide-hairline" aria-label="本机节点">
        {nodes.map((n) => {
          const runtime = hostPresentation(n.host)
          const cards = nodeCardData(n, undefined)
          return (
            <li key={n.id} className="px-3 py-2.5 hover:bg-surface-2">
              <div className="flex items-start gap-2.5">
                <StatusDot status={runtime.status === 'online' ? 'online' : runtime.status === 'warning' ? 'warning' : 'idle'} className="mt-1.5" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                    <span className="zrcin-break font-mono text-[13px] font-medium leading-5 text-ink">{n.id}</span>
                    <span className={cn('text-xs', runtime.status === 'online' ? 'text-success' : runtime.status === 'warning' ? 'text-warning' : 'text-ink-3')}>
                      {runtime.label}
                    </span>
                    {n.project_default && <span className="text-[11px] text-ink-3">项目默认</span>}
                  </div>
                  {n.endpoint && <p className="zrcin-break mt-1 font-mono text-[11px] leading-4 text-ink-3">{n.endpoint}</p>}
                  {n.host && !n.host.ready && (
                    <p className="zrcin-break mt-1 whitespace-pre-wrap text-[12px] leading-relaxed text-destructive">
                      {n.host.last_error || '宿主正在启动，等待模型就绪。'}
                      <span className="text-ink-3"> 修好后到设置 → 模型保存重试。</span>
                    </p>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant={terminal === n.id ? 'soft' : 'outline'}
                    size="xs"
                    active={terminal === n.id}
                    disabled={!n.terminal}
                    aria-label={`打开 ${n.id} 的终端`}
                    onClick={() => onTerminal(terminal === n.id ? null : n.id)}
                  >
                    <SquareTerminal className="h-3 w-3" aria-hidden="true" />
                    终端
                  </Button>
                  <RowMenu node={n} title={n.id} boundIds={boundIds} projectDefault={projectDefault} onChanged={onChanged} />
                </div>
              </div>
              <NodeCards
                data={{...cards, tone: healthTone(cards.health.state), label: healthLabel(cards.health.state)}}
                ariaLabel={`${n.id} 的详情卡片`}
              />
            </li>
          )
        })}
      </ul>
    </Panel>
  )
}

function osArch(os?: string, arch?: string): string {
  if (!os && !arch) return ''
  return [os, arch].filter(Boolean).join('/')
}
