import { useRef, useState } from 'react'
import { Activity, ArrowUpRight, Download, FolderKanban, Pencil, Play, Plus, RefreshCw, Trash2, TriangleAlert, Upload } from 'lucide-react'
import { cn } from '@cyber/theme'
import {
  Badge,
  Button,
  Callout,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Field,
  SkeletonList,
  Spinner,
  StatTile,
  StatusIndicator,
  Textarea,
} from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { NewProjectDialog } from '@/components/NewProjectDialog'
import { DeleteProjectDialog } from '@/components/DeleteProjectDialog'
import { api, gotoProject } from '@/lib/api'
import { fmtInt, fmtTime } from '@/lib/format'
import type { ProjectInfo } from '@/lib/types'

function ProjectMetric({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="min-w-0 px-2 py-2.5 text-center" title={title}>
      <dt className="text-[12px] leading-4 text-ink-2">{label}</dt>
      <dd className="tw-nums mt-1 truncate font-mono text-[15px] font-semibold leading-5 text-ink">{value}</dd>
    </div>
  )
}

export function Projects({
  projects,
  current,
  onChanged,
  onDeleted,
  refreshing,
  loading,
}: {
  projects: ProjectInfo[]
  current: string
  onChanged: () => void
  onDeleted: (id: string) => void
  refreshing: boolean
  loading: boolean
}) {
  const [editing, setEditing] = useState<ProjectInfo | null>(null)
  const [scope, setScope] = useState('')
  const [description, setDescription] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<ProjectInfo | null>(null)
  // Whole-project transfer: export downloads one .twproj; import uploads one.
  // Large files are streamed by the browser, never parsed in the page.
  const [exporting, setExporting] = useState('')
  const [importing, setImporting] = useState(false)
  const [transferError, setTransferError] = useState('')
  const fileInput = useRef<HTMLInputElement>(null)
  const activeProjects = projects.filter(project => project.running > 0).length
  const running = projects.reduce((total, project) => total + project.running, 0)

  function openScope(p: ProjectInfo) {
    setEditing(p)
    setScope(p.scope ?? '')
    setDescription(p.description ?? '')
    setError('')
  }

  async function saveScope() {
    if (!editing || busy) return
    setError('')
    setBusy(true)
    try {
      await api.updateProject(editing.id, { scope: scope.trim(), description: description.trim() })
      setEditing(null)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  function deleted(id: string) {
    onDeleted(id)
    if (id === current) {
      const next = projects.find(p => p.id !== id)
      if (next) gotoProject(next.id)
      else window.location.assign('/g/?view=projects')
    }
  }

  async function exportProject(p: ProjectInfo) {
    if (exporting) return
    setExporting(p.id)
    setTransferError('')
    try {
      const res = await api.exportProject(p.id)
      // The endpoint answers with a download path; the browser saves the
      // attachment and stays on this page.
      window.location.assign(api.projectExportURL(res.name))
    } catch (e) {
      setTransferError(e instanceof Error ? e.message : String(e))
    } finally {
      setExporting('')
    }
  }

  async function importFile(file: File | undefined) {
    if (!file || importing) return
    setImporting(true)
    setTransferError('')
    try {
      const res = await api.importProject(file)
      onChanged()
      gotoProject(res.project.id)
    } catch (e) {
      setTransferError(e instanceof Error ? e.message : String(e))
    } finally {
      setImporting(false)
      if (fileInput.current) fileInput.current.value = ''
    }
  }

  return (
    <Page
      title="项目"
      subtitle="管理项目及其账本，支持 .twproj 文件导入与导出。"
      className="@container overscroll-contain"
      actions={
        <>
          <Button
            size="icon-sm"
            variant="ghost"
            onClick={onChanged}
            disabled={loading || refreshing}
            aria-label="刷新项目列表"
            aria-busy={refreshing}
            title="重新拉取项目列表"
          >
            <RefreshCw className={cn('h-3.5 w-3.5', refreshing && 'motion-safe:animate-spin')} />
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept=".twproj,application/zip"
            className="hidden"
            aria-hidden="true"
            onChange={(e) => void importFile(e.target.files?.[0])}
          />
          <Button size="sm" variant="outline" onClick={() => fileInput.current?.click()} disabled={importing} aria-busy={importing}>
            {importing ? <Spinner size="sm" className="h-3.5 w-3.5" /> : <Upload className="h-3.5 w-3.5" />}
            {importing ? '导入中…' : '导入项目'}
          </Button>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="h-3.5 w-3.5" /> 新建项目
          </Button>
        </>
      }
    >
      <div className="mx-auto max-w-[1440px] space-y-6">
        <span role="status" aria-live="polite" className="sr-only">
          {loading ? '读取项目中' : refreshing ? '刷新项目中' : `共 ${fmtInt(projects.length)} 个项目`}
        </span>
        {projects.length > 0 && (
          <section aria-label="项目统计" className="tw-panel overflow-hidden rounded-xl">
            <div className="tw-metric-grid grid-cols-3">
              <StatTile size="lg" label="项目总数" value={fmtInt(projects.length)} icon={<FolderKanban aria-hidden="true" className="size-3.5" />} />
              <StatTile size="lg" label="活跃项目" value={fmtInt(activeProjects)} icon={<Activity aria-hidden="true" className="size-3.5" />} />
              <StatTile size="lg" label="正在运行" value={fmtInt(running)} tone={running > 0 ? 'success' : 'default'} icon={<Play aria-hidden="true" className="size-3.5" />} />
            </div>
          </section>
        )}

        <section aria-labelledby="project-list-heading" aria-busy={loading || refreshing}>
          <div className="mb-3 flex min-h-6 items-center justify-between gap-2">
            <h2 id="project-list-heading" className="text-[15px] font-semibold text-ink">项目列表</h2>
            {(loading || refreshing) && <span className="text-caption text-ink-2">{refreshing ? '刷新中…' : '读取中…'}</span>}
          </div>
          {loading && projects.length === 0 ? (
            <Panel>
              <SkeletonList count={4} />
            </Panel>
          ) : projects.length === 0 ? (
            <Panel>
              <EmptyState icon={FolderKanban} title="还没有项目" description="可新建项目，也可以导入已有的 .twproj 文件。" />
            </Panel>
          ) : (
            <ul role="list" className="grid grid-cols-1 gap-4 @3xl:grid-cols-2 @7xl:grid-cols-3" aria-label="项目列表">
              {projects.map((p, index) => {
                const open = p.id === current
                return (
                  <li
                    key={p.id}
                    className={cn(
                      'tw-panel tw-project-card flex min-w-0 flex-col gap-4 rounded-xl p-4 transition-colors sm:p-5',
                      index >= 8 && '[contain-intrinsic-size:auto_13rem] [content-visibility:auto]',
                    )}
                  >
                    <div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2">
                      <span className="tw-project-emblem grid size-10 shrink-0 place-items-center rounded-lg text-primary" aria-hidden="true">
                        <FolderKanban className="size-[18px]" />
                      </span>
                      <div className="min-w-0 flex-[1_1_9rem]">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                          <h3 className="tw-break line-clamp-2 text-[16px] font-semibold leading-6 text-ink" title={p.name}>{p.name}</h3>
                          {open && (
                            <Badge variant="primary" size="sm" className="shrink-0">
                              当前
                            </Badge>
                          )}
                        </div>
                        <p className="tw-break mt-0.5 line-clamp-2 text-caption text-ink-2" title={p.scope}>{p.scope || '未指定授权边界'}</p>
                      </div>
                      <StatusIndicator
                        status={p.running > 0 ? 'running' : 'idle'}
                        label={p.running > 0 ? `${fmtInt(p.running)} 在跑` : '无运行'}
                        compact
                        className="shrink-0 rounded-full border border-hairline bg-surface-1 px-2 py-1"
                      />
                    </div>

                    <dl className="tw-inset mt-auto grid grid-cols-4 divide-x divide-hairline overflow-hidden rounded-lg">
                      <ProjectMetric label="意图" value={fmtInt(p.intents)} />
                      <ProjectMetric label="事实" value={fmtInt(p.facts)} />
                      <ProjectMetric label="证据" value={fmtInt(p.evidence)} />
                      <ProjectMetric
                        label="并发"
                        value={p.max_runs_effective === 0 ? '不限' : fmtInt(p.max_runs_effective)}
                        title={p.max_runs == null ? `继承实例默认（${p.max_runs_effective}）` : '项目覆盖'}
                      />
                    </dl>

                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="min-w-0 text-[11px] leading-4 text-ink-3">
                        <div className="tw-break font-mono">{p.id}</div>
                        <div className="tw-nums" title="创建时间">{fmtTime(p.created)}</div>
                      </div>
                      <div className="ml-auto flex shrink-0 items-center gap-1">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          onClick={() => void exportProject(p)}
                          disabled={exporting === p.id}
                          aria-label={`导出项目「${p.name}」`}
                          title={p.running > 0 ? '导出项目（先暂停并等待运行结束）' : '导出项目为 .twproj 包'}
                        >
                          {exporting === p.id ? <Spinner size="sm" className="h-3.5 w-3.5" /> : <Download className="h-3.5 w-3.5" />}
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          onClick={() => openScope(p)}
                          aria-label={`编辑项目「${p.name}」`}
                          title="改授权边界（当前口径）"
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          className="text-ink-2 hover:bg-destructive/10 hover:text-destructive"
                          onClick={() => setDeleting(p)}
                          aria-label={`删除项目「${p.name}」`}
                          title="删除项目"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                        {!open && (
                          <Button size="sm" variant="outline" className="ml-1" onClick={() => gotoProject(p.id)}>
                            打开 <ArrowUpRight aria-hidden="true" className="h-3.5 w-3.5" />
                          </Button>
                        )}
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
        </section>

        {transferError && (
          <div role="alert">
            <Callout tone="destructive" icon={<TriangleAlert className="h-4 w-4" />}>
              {transferError}
            </Callout>
          </div>
        )}

        <details className="rounded-lg border border-hairline bg-surface-1 px-4 py-3 open:space-y-2">
          <summary className="cursor-pointer text-caption font-medium text-ink-2">导入、导出与删除说明</summary>
          <p className="max-w-[78ch] text-caption text-ink-2">
            导出会打包项目清单、账本、状态笔记、运行日志、证据与登录材料，生成单个 <span className="font-mono">.twproj</span>。导出前请先暂停项目并等待运行结束。导入会新建一个暂停的项目，同名项目可以并存。删除项目会停止运行并清理事实意图图、工作区与运行日志，操作前会二次确认。已镜像条目保留在漏洞库中，全局工具也会保留。
          </p>
        </details>
      </div>

      <NewProjectDialog open={creating} onOpenChange={setCreating} onCreated={onChanged} />
      {deleting && <DeleteProjectDialog project={deleting} onClose={() => setDeleting(null)} onDeleted={deleted} />}

      <Dialog open={!!editing} onOpenChange={(o) => { if (!o && !busy) setEditing(null) }}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>授权边界 · {editing?.name}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <Field
              label="当前口径（可动态补充）"
              hint="写入派发简报和指挥官决策；资产动态增删时用通信页补充即可，不必改这里。程序不解析、不逐资产校验。"
            >
              <Textarea
                rows={3}
                value={scope}
                disabled={busy}
                onChange={(e) => setScope(e.target.value)}
                className="font-mono text-[12px]"
              />
            </Field>
            <Field
              label="项目描述（可选）"
              hint="平台规则、禁止动作、目标材料口径；原样带进简报和决策，不解析、不校验。"
            >
              <Textarea
                rows={2}
                value={description}
                disabled={busy}
                onChange={(e) => setDescription(e.target.value)}
                className="text-[12px]"
              />
            </Field>
            {error && (
              <div role="alert">
                <Callout tone="destructive" icon={<TriangleAlert className="h-4 w-4" />}>
                  保存失败：{error}
                </Callout>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => setEditing(null)}>
              取消
            </Button>
            <Button onClick={saveScope} disabled={busy} aria-busy={busy}>
              {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
              {busy ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Page>
  )
}
