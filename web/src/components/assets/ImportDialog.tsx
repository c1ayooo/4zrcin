// The import surface (spec #1 工单8/#9): parse first, judge against the fence,
// let the operator check the out-of-fence hosts, then write the fence rows and
// the batch in that order. Opening the preview twice changes nothing; the
// dialog says so by never writing before 确认.
//
// Both import kinds share this dialog because they share the question: which
// of these hosts is the engagement actually allowed to touch?

import { useMemo, useState } from 'react'
import { Upload } from 'lucide-react'
import {
  Badge,
  Button,
  Callout,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Input,
  SkeletonList,
  Spinner,
  ToggleGroup,
  ToggleGroupItem,
} from '@cyber/ui'
import { api } from '@/lib/api'
import {
  groupImportPreview,
  importConfirmProblem,
  importKindLabel,
  isSelectable,
  selectAllOutOfFence,
  selectionImpact,
  toggleImportHost,
  type ImportKind,
} from '@/lib/import-preview'
import type { ImportPreview, ImportResult } from '@/lib/types'

export function ImportDialog({ onClose, onImported }: { onClose: () => void; onImported: () => void }) {
  const [kind, setKind] = useState<ImportKind>('assets')
  const [path, setPath] = useState('')
  const [preview, setPreview] = useState<ImportPreview>()
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [receipt, setReceipt] = useState('')

  const groups = useMemo(() => groupImportPreview(preview), [preview])
  const impact = useMemo(() => selectionImpact(preview, selected), [preview, selected])
  const problem = importConfirmProblem(preview, path, busy)

  const reset = () => {
    setPreview(undefined)
    setSelected(new Set())
    setReceipt('')
    setError('')
  }

  async function loadPreview() {
    if (loading || !path.trim()) {
      if (!path.trim()) setError('填写服务器上的文件路径。')
      return
    }
    setLoading(true)
    setError('')
    setReceipt('')
    setSelected(new Set())
    setPreview(undefined)
    try {
      const next = kind === 'traffic' ? await api.trafficImportPreview(path.trim()) : await api.assetImportPreview(path.trim())
      setPreview(next)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  async function confirm() {
    if (busy || problem || !preview) return
    setBusy(true)
    setError('')
    try {
      const hosts = impact.fenceHosts
      const result: ImportResult = kind === 'traffic' ? await api.importTraffic(path.trim(), hosts) : await api.importAssets(path.trim(), hosts)
      const records = Number(result.records ?? result.entries ?? preview.records)
      const hostCount = Number(result.hosts ?? preview.hosts)
      const added = Number(result.fence_added ?? 0)
      setReceipt(
        `${importKindLabel(kind)}导入完成：${records} 条记录 · ${hostCount} 个主机` +
          (added > 0 ? ` · ${added} 个主机加入围栏` : ''),
      )
      setSelected(new Set())
      setPreview(undefined)
      onImported()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto overscroll-contain" aria-busy={busy}>
        <DialogHeader>
          <DialogTitle>导入</DialogTitle>
          <DialogDescription>
            先预览：解析后按项目围栏逐条判定，预览阶段不写账。勾选超出围栏的主机会先把它们加入围栏，再整批落库。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <ToggleGroup
            value={kind}
            onValueChange={(value) => {
              setKind(value as ImportKind)
              reset()
            }}
            disabled={busy}
            ariaLabel="导入类型"
          >
            <ToggleGroupItem value="assets">扫描结果</ToggleGroupItem>
            <ToggleGroupItem value="traffic">HAR 流量</ToggleGroupItem>
          </ToggleGroup>

          <Field
            label="文件路径"
            hint={kind === 'traffic' ? 'HAR 文件在服务器上的位置；相对路径按案例目录解析。' : '扫描结果（JSONL/数组）在服务器上的位置；相对路径按案例目录解析。'}
          >
            <div className="flex gap-2">
              <Input
                value={path}
                disabled={loading || busy}
                onChange={(e) => {
                  setPath(e.target.value)
                  reset()
                }}
                placeholder={kind === 'traffic' ? 'traffic/capture.har' : 'scan/result.jsonl'}
                aria-label="导入文件路径"
                className="h-8 font-mono text-[12px]"
                spellCheck={false}
              />
              <Button size="sm" variant="outline" onClick={() => void loadPreview()} disabled={loading || busy}>
                {loading && <Spinner size="sm" className="mr-1" />}
                {loading ? '解析中…' : '预览'}
              </Button>
            </div>
          </Field>

          {error && <Callout tone="destructive">{error}</Callout>}

          {loading && !preview && <SkeletonList count={3} />}

          {preview && (
            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2 text-[11.5px] text-ink-2">
                <Badge size="sm" variant="secondary">{importKindLabel(kind)}</Badge>
                <span className="zrcin-nums font-mono">{preview.records} 条记录 · {preview.hosts} 个主机</span>
                {preview.truncated ? <span className="text-warning">超出上限的 {preview.truncated} 个主机不会导入</span> : null}
              </div>

              <section aria-label="围栏内" className="rounded-lg border border-hairline px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden="true" />
                  <span className="text-[12px] text-ink">围栏内</span>
                  <span className="zrcin-nums ml-auto font-mono text-[11px] text-ink-2">{preview.in_fence}</span>
                </div>
                {groups.inFence.length > 0 && (
                  <p className="zrcin-break mt-1 font-mono text-[10.5px] leading-4 text-ink-3">
                    {groups.inFence.slice(0, 8).map((item) => item.host).join(' · ')}
                    {groups.inFence.length > 8 ? ` · 另有 ${groups.inFence.length - 8} 个` : ''}
                  </p>
                )}
              </section>

              <section aria-label="超出围栏" className="rounded-lg border border-hairline px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="h-1.5 w-1.5 rounded-full bg-warning" aria-hidden="true" />
                  <span className="text-[12px] text-ink">超出围栏</span>
                  <span className="zrcin-nums ml-auto font-mono text-[11px] text-ink-2">
                    {impact.fenceCount}/{groups.outOfFence.length}
                  </span>
                  {groups.outOfFence.length > 0 && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="px-1.5"
                      onClick={() => setSelected(selectAllOutOfFence(groups, selected))}
                      disabled={busy}
                    >
                      {impact.allSelected ? '取消全选' : '全选'}
                    </Button>
                  )}
                </div>
                {groups.outOfFence.length === 0 ? (
                  <p className="mt-1 text-[11px] text-ink-3">没有超出围栏的主机。</p>
                ) : (
                  <ul role="list" className="mt-1.5 max-h-56 space-y-1 overflow-y-auto overscroll-contain [scrollbar-width:thin]">
                    {groups.outOfFence.map((item) => {
                      const selectable = isSelectable(item)
                      return (
                        <li key={item.host} className="flex items-center gap-2 rounded-md px-1 py-1 hover:bg-surface-2">
                          <Checkbox
                            checked={selected.has(item.host)}
                            onCheckedChange={() => setSelected(toggleImportHost(selected, item.host, selectable))}
                            aria-label={`加入围栏 ${item.host}`}
                          />
                          <span className="zrcin-break min-w-0 flex-1 font-mono text-[11.5px] text-ink">{item.host}</span>
                          <span className="zrcin-nums shrink-0 font-mono text-[10.5px] text-ink-3">
                            {item.ports ? `${item.ports} 端口` : ''}
                            {item.ports && item.requests ? ' · ' : ''}
                            {item.requests ? `${item.requests} 请求` : ''}
                          </span>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </section>

              {(preview.unjudged > 0 || !preview.fence_enabled) && (
              <section aria-label="无法判定" className="rounded-lg border border-hairline px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="h-1.5 w-1.5 rounded-full bg-ink-3" aria-hidden="true" />
                  <span className="text-[12px] text-ink">无法判定</span>
                  <span className="zrcin-nums ml-auto font-mono text-[11px] text-ink-2">{preview.unjudged}</span>
                </div>
                <p className="mt-1 text-[11px] leading-4 text-ink-3">
                  {preview.fence_enabled
                    ? '这些主机的身份不在规则表的判定范围里。'
                    : '项目未启用围栏：无法判定围栏内外，这次导入不会改动围栏。'}
                </p>
              </section>
              )}

              <p className="text-[11px] leading-4 text-ink-3">
                {impact.fenceCount > 0
                  ? `勾选的 ${impact.fenceCount} 个主机先写入围栏（并落资产），随后整批导入。`
                  : '不勾选：整批直接导入，围栏不变。'}
              </p>
            </div>
          )}

          {receipt && <p className="text-[12px] leading-5 text-ink-2" data-import-receipt>{receipt}</p>}
        </div>

        <DialogFooter>
          <Button size="sm" variant="ghost" onClick={onClose} disabled={busy}>
            {receipt ? '关闭' : '取消'}
          </Button>
          <Button size="sm" className="gap-1.5" onClick={() => void confirm()} disabled={Boolean(problem)}>
            {busy ? <Spinner size="sm" /> : <Upload className="h-3.5 w-3.5" />}
            {busy ? '导入中…' : impact.fenceCount > 0 ? `确认导入（${impact.fenceCount} 项加入围栏）` : '确认导入'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
