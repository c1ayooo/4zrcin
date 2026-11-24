import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Callout, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, Input, Spinner, Switch } from '@cyber/ui'
import { Panel } from './Page'
import { api } from '@/lib/api'
import { durationLabel, mergeProjectSettings, projectSettingsDraft, projectSettingsPatch } from '@/lib/settings'
import type { ProjectInfo } from '@/lib/types'

/**
 * One project's defaults. Every field is optional: an empty value means "follow
 * the instance", and the effective value is spelled out in the hint so the
 * operator never has to guess what a blank will do at dispatch time.
 */
export function ProjectSettingsCard({ project, defaults, onSaved, onDirty }: {
  project: ProjectInfo
  defaults: { max_runs: number; round_timeout_seconds: number; reviews_disabled_default?: boolean }
  onSaved: () => void
  onDirty: (id: string, dirty: boolean) => void
}) {
  const baseline = useRef(projectSettingsDraft(project))
  const [draft, setDraft] = useState(baseline.current)
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [fieldError, setFieldError] = useState<'max_runs' | null>(null)
  // The review switch is a runtime control, not a draft field: it takes effect on
  // the server the moment it flips, so it is not part of the draft/dirty/save flow.
  const [reviewBusy, setReviewBusy] = useState(false)
  const [reviewError, setReviewError] = useState('')
  const [reviewNote, setReviewNote] = useState('')
  const [confirmClose, setConfirmClose] = useState(false)
  const maxRunsRef = useRef<HTMLInputElement>(null)
  const dirty = Object.keys(draft).some(key => draft[key as keyof typeof draft] !== baseline.current[key as keyof typeof draft])
  // Reviews have two levels: an instance default and an optional per-project
  // override. `reviews_disabled` is the value in force (a running project reports
  // the switch it actually runs with), and `reviews_disabled_override` is this
  // project's own stored value: `null`/absent means "no override", so the project
  // keeps following the instance, including later changes. The instance default is
  // read at attach, so a running project can legitimately differ from it.
  const instanceReviewsOff = defaults.reviews_disabled_default ?? false
  const reviewsOverridden = project.reviews_disabled_override != null
  const reviewsInForce = !(project.reviews_disabled ?? project.reviews_disabled_override ?? instanceReviewsOff)

  useEffect(() => {
    const incoming = projectSettingsDraft(project)
    const previous = baseline.current
    baseline.current = incoming
    setDraft(current => mergeProjectSettings(previous, incoming, current))
  }, [project])
  useEffect(() => { onDirty(project.id, dirty) }, [project.id, dirty, onDirty])
  useEffect(() => () => onDirty(project.id, false), [project.id, onDirty])

  async function save() {
    if (lock.current) return
    // The concurrency draft is the one field this card can reject locally; the
    // request would otherwise fail with a payload-level message and no field to
    // land on. Validate first, mark the field and move the focus there.
    const maxRuns = draft.max_runs.trim()
    if (maxRuns && !/^\d+$/.test(maxRuns)) {
      setFieldError('max_runs')
      setError('执行者并发必须是非负整数，留空表示跟随实例。')
      setNote('')
      maxRunsRef.current?.focus()
      return
    }
    lock.current = true
    setBusy(true); setError(''); setNote(''); setFieldError(null)
    try {
      const patch = projectSettingsPatch(baseline.current, draft)
      if (!Object.keys(patch).length) return
      const saved = await api.updateProject(project.id, patch)
      baseline.current = projectSettingsDraft(saved)
      setDraft(baseline.current)
      setNote('已保存。并发用于后续派发，单轮时限用于下一轮决策；起点、终点与内置约束请在「项目文档」页改。')
      onSaved()
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { lock.current = false; setBusy(false) }
  }

  /**
   * The review switch, project-scoped: it writes this project's own override
   * through PUT /api/projects/{id}. The server applies it immediately when the
   * project is attached — closing cancels live review runs, opening re-queues
   * them — so this is a runtime control, not part of the draft/save flow.
   * Closing is confirmed first because it cancels live reviews; opening is safe
   * to do directly (it only re-arms what the close suspended).
   */
  async function setReviews(on: boolean) {
    if (reviewBusy) return
    setReviewBusy(true); setReviewError(''); setReviewNote('')
    try {
      await api.updateProject(project.id, { reviews_disabled: !on })
      setReviewNote(on
        ? '复核已开启，并把本项目固定为开启（不再跟随实例默认）；未复核事实会重新排队复核。'
        : '复核已关闭，并把本项目固定为关闭；正在跑的复核已取消，未复核事实按原文可见。')
      onSaved()
    } catch (e) {
      setReviewError(`${on ? '开启' : '关闭'}复核失败：${e instanceof Error ? e.message : String(e)}`)
    } finally { setReviewBusy(false) }
  }

  /** Drop the override so the project follows the instance default again. The
   *  runtime value is whatever the default says; the server recomputes it. */
  async function followInstance() {
    if (reviewBusy) return
    setReviewBusy(true); setReviewError(''); setReviewNote('')
    try {
      await api.updateProject(project.id, { reviews_disabled: null })
      setReviewNote('已恢复跟随实例：之后实例默认怎么变，这个项目就怎么变。')
      onSaved()
    } catch (e) {
      setReviewError(`恢复跟随失败：${e instanceof Error ? e.message : String(e)}`)
    } finally { setReviewBusy(false) }
  }

  return <Panel
    title={<span className="break-all">{project.name}</span>}
    extra={
      <div className="flex flex-wrap items-center justify-end gap-1.5">
        <Badge variant="muted" size="sm">执行中 {project.running}</Badge>
        {dirty && <Badge variant="warning" size="sm">未保存</Badge>}
      </div>
    }
    bodyClassName="@container space-y-4"
  >
    <div className="break-all font-mono text-[11px] leading-4 text-ink-3">{project.id}</div>
    <fieldset disabled={busy} className="min-w-0 space-y-4">
      <Callout tone="info" className="leading-relaxed">
        <div>起点、终点与内置约束是项目的三份文档，在「项目文档」页整篇编辑：每次保存留一版历史，并带上读到的修订号，别人刚改过会提示刷新而不是覆盖。</div>
        <a
          href="?view=docs"
          className="mt-1 inline-flex text-ink-2 underline decoration-hairline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        >
          去「项目文档」页改这三份文档
        </a>
      </Callout>
      <div className="grid min-w-0 gap-4 @md:grid-cols-2">
        <Field
          label="执行者并发"
          hint={`留空跟随实例（${defaults.max_runs || '不限'}）；0 表示不限`}
          error={fieldError === 'max_runs' ? '必须是非负整数；留空表示跟随实例。' : undefined}
        >
          <Input ref={maxRunsRef} aria-label={`${project.name} 的执行者并发`} aria-invalid={fieldError === 'max_runs' ? true : undefined} data-user-invalid={(fieldError === 'max_runs' ? true : undefined) ? 'true' : undefined} type="number" min={0} step={1} inputMode="numeric" enterKeyHint="next" value={draft.max_runs} placeholder="跟随实例"
            onChange={e => { setFieldError(null); setDraft(current => ({ ...current, max_runs: e.target.value })) }} />
        </Field>
        <Field label="指挥官单轮时限" hint={`留空跟随实例（${durationLabel(defaults.round_timeout_seconds)}）；0 表示不限`}>
          <Input aria-label={`${project.name} 的指挥官单轮时限`} spellCheck={false} autoCapitalize="none" enterKeyHint="done" value={draft.round_timeout} placeholder="例如 15m / 90s"
            onChange={e => setDraft(current => ({ ...current, round_timeout: e.target.value }))} />
        </Field>
      </div>
      <div className={reviewsInForce ? 'rounded-lg border border-hairline bg-surface-2/40' : 'rounded-lg border border-warning/40 bg-warning/5'}>
        <div className="flex items-start gap-3 px-3 py-2.5">
          <Switch
            id={`reviews-${project.id}`}
            checked={reviewsInForce}
            disabled={busy || reviewBusy}
            onCheckedChange={(next) => { if (next) void setReviews(true); else setConfirmClose(true) }}
          />
          <label
            htmlFor={`reviews-${project.id}`}
            className="min-w-0 flex-1 cursor-pointer text-[12px] leading-relaxed text-ink-2"
          >
            <span className="block font-medium text-ink">
              复核（verified_only） · {reviewsOverridden
                ? `已覆盖：${reviewsInForce ? '开启' : '关闭'}（实例默认：${instanceReviewsOff ? '关闭' : '开启'}）`
                : `跟随实例（实例默认：${instanceReviewsOff ? '关闭' : '开启'}）`}
            </span>
            <span className="block">
              本项目当前生效：{reviewsInForce ? '开启' : '关闭'}。两层：实例默认值在设置页，这一项只在需要例外时覆盖它。开启/关闭立即生效，关闭会取消正在跑的复核。
            </span>
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-2 border-t border-hairline px-3 py-2">
          <span className="text-[11px] leading-4 text-ink-3">
            {reviewsOverridden
              ? '这是本项目的覆盖值，不随实例默认变化。'
              : '没有覆盖值：之后实例默认怎么变，这个项目就怎么变。实例默认在项目启动、新建或重开时读取，改默认不会切换正在运行的项目。'}
          </span>
          {reviewsOverridden && (
            <Button size="xs" variant="ghost" disabled={busy || reviewBusy} onClick={() => void followInstance()}>
              恢复跟随实例
            </Button>
          )}
        </div>
        <div className="border-t border-hairline px-3 py-2 text-[11px] leading-relaxed text-ink-3">
          {!reviewsInForce
            ? '关闭后：意图结束不再排队复核，未复核事实按原文可见（状态仍记为 unverified）。准入仍有限制：事实类型必须可复核（note 等不可作前提），withdrawn / rejected 不可用；从未登记原始证据只作标注、不拦派发；已登记的原件若缺失或摘要不符，会阻止派发并在概览告警。'
            : '开启时：每个意图结束后，新的 vuln/cred 结论先由独立复核者重跑；通过后才 done，未通过进入 review_failed。host/service 等只在下游把事实当必要前提时按需复核。'}
        </div>
        {(reviewNote || reviewError) && (
          <div className="space-y-1.5 border-t border-hairline px-3 py-2">
            {reviewNote && <div role="status" className="text-[11px] leading-relaxed text-ink-2">{reviewNote}</div>}
            {reviewError && <Callout tone="destructive">{reviewError}</Callout>}
          </div>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t border-hairline pt-4">
        <Button size="sm" variant="soft" onClick={() => void save()} disabled={busy || !dirty} aria-busy={busy}>
          {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
          {busy ? '保存中…' : '保存项目设置'}
        </Button>
        <Button size="sm" variant="ghost" disabled={busy || !dirty} onClick={() => { setDraft(baseline.current); setError(''); setNote('') }}>撤销改动</Button>
      </div>
    </fieldset>
    <Dialog open={confirmClose} onOpenChange={(next) => { if (!reviewBusy) setConfirmClose(next) }}>
      <DialogContent className="max-h-[90dvh] max-w-[calc(100%-2rem)] overflow-y-auto overscroll-contain sm:max-w-md" aria-busy={reviewBusy}>
        <DialogHeader>
          <DialogTitle>关闭复核？</DialogTitle>
          <DialogDescription>
            「{project.name}」会立刻停止复核：正在跑的复核被取消，之后不再产生复核 run。
          </DialogDescription>
        </DialogHeader>
        <Callout tone="warning">
          <div className="space-y-1 text-[12px] leading-relaxed">
            <p>未复核事实改为按原文可见，状态仍记为 unverified；准入仍要求事实类型可复核，withdrawn / rejected 不可用。</p>
            <p>这会把这个项目钉成「覆盖：关闭」，之后实例默认怎么变它都不跟随；需要跟随实例时用「恢复跟随实例」。</p>
          </div>
        </Callout>
        <DialogFooter>
          <Button variant="outline" disabled={reviewBusy} onClick={() => setConfirmClose(false)}>保持开启</Button>
          <Button
            variant="destructive"
            disabled={reviewBusy}
            onClick={() => { setConfirmClose(false); void setReviews(false) }}
          >
            {reviewBusy ? '关闭中…' : '关闭复核'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <div aria-live="polite" className="space-y-2">
      {note && <Callout tone="success">{note}</Callout>}
      {error && <Callout tone="destructive">保存失败：{error}</Callout>}
    </div>
  </Panel>
}
