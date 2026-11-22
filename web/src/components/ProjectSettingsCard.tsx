import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Callout, Field, Input, Spinner } from '@cyber/ui'
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
  defaults: { max_runs: number; round_timeout_seconds: number }
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
  const maxRunsRef = useRef<HTMLInputElement>(null)
  const dirty = Object.keys(draft).some(key => draft[key as keyof typeof draft] !== baseline.current[key as keyof typeof draft])

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
      <Callout tone={project.reviews_disabled ? 'warning' : 'info'} className="leading-relaxed">
        <div className="font-semibold">{project.reviews_disabled ? '复核已关闭' : '固定复核（verified_only）'}</div>
        <div className="mt-1">
          {project.reviews_disabled
            ? '本项目不会再有复核 run：意图结束不再排队固定复核，未核验事实按原文可见（状态仍记为 unverified）。准入仍有限制：事实类型必须可复核（note 等不可作前提）、必须带保全的原始证据，withdrawn / rejected 不可用。需要复核时在项目页「结论」的复核面板里开启。'
            : '每个意图结束后，新的 vuln/cred 结论先由独立复核者重跑；通过后才 done，未通过进入 review_failed。host/service 等只在下游把事实当必要前提时按需复核。没有自动复核开关，也没有 standard 模式。'}
        </div>
      </Callout>
      <div className="flex flex-wrap items-center gap-2 border-t border-hairline pt-4">
        <Button size="sm" variant="soft" onClick={() => void save()} disabled={busy || !dirty} aria-busy={busy}>
          {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
          {busy ? '保存中…' : '保存项目设置'}
        </Button>
        <Button size="sm" variant="ghost" disabled={busy || !dirty} onClick={() => { setDraft(baseline.current); setError(''); setNote('') }}>撤销改动</Button>
      </div>
    </fieldset>
    <div aria-live="polite" className="space-y-2">
      {note && <Callout tone="success">{note}</Callout>}
      {error && <Callout tone="destructive">保存失败：{error}</Callout>}
    </div>
  </Panel>
}
