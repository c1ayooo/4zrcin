// Operator-authored bootstrap: a display name, the starting point (起点), the
// end state (终点), and the built-in constraint document.
//
// The name is only a display name: the directory id is a short random id
// (p-xxxxxxxx) decoupled from it, so renaming a project never moves its
// directory. 起点 becomes the project brief (scope); 终点 becomes the project's
// single endpoint goal at creation time — the commander never derives or splits
// either one. The standing constraints are a prefilled document the operator may
// edit or cancel (it becomes the project description).
import { useEffect, useRef, useState } from 'react'
import {
  Button,
  Callout,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Input,
  Spinner,
  Switch,
  Textarea,
} from '@cyber/ui'
import { api } from '@/lib/api'
import { DEFAULT_PROJECT_CONSTRAINTS, projectDescriptionDraft } from '@/lib/project-defaults'

type RequiredField = 'name' | 'start' | 'end'

const REQUIRED_FIELDS: { field: RequiredField; message: string }[] = [
  { field: 'name', message: '项目名称不能为空：请填写一个显示名（不能用空白）。' },
  { field: 'start', message: '先写起点：它是项目简报，统领授权范围与已知状态。' },
  { field: 'end', message: '终点必填：它要能判断这次测试是否完成。' },
]

export function NewProjectDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated?: () => void
}) {
  // The display name: required, and the only thing a rename would change.
  const [projectName, setProjectName] = useState('')
  // Where we start: becomes the project brief.
  const [start, setStart] = useState('')
  // What "done" means: becomes the project's single endpoint goal.
  const [end, setEnd] = useState('')
  // The built-in constraint document is on by default, editable, and can be
  // cancelled (off = no description); turning it back on restores the default.
  const [constraintsOn, setConstraintsOn] = useState(true)
  const [constraints, setConstraints] = useState(DEFAULT_PROJECT_CONSTRAINTS)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // The name is only flagged after a submit attempt: opening an empty form must
  // not look like the operator already made a mistake.
  const [attempted, setAttempted] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<RequiredField, string>>>({})
  const nameRef = useRef<HTMLInputElement>(null)
  const startRef = useRef<HTMLTextAreaElement>(null)
  const endRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (open) {
      setAttempted(false)
      setFieldErrors({})
    }
  }, [open])

  const constraintsEdited = constraints !== DEFAULT_PROJECT_CONSTRAINTS

  function toggleConstraints(on: boolean) {
    setConstraintsOn(on)
    setConstraints(on ? DEFAULT_PROJECT_CONSTRAINTS : '')
  }

  /** First failing required field, in visual order, or null when complete. */
  function firstProblem(): { field: RequiredField; message: string } | null {
    if (!projectName.trim()) return REQUIRED_FIELDS[0]
    if (!start.trim()) return REQUIRED_FIELDS[1]
    if (!end.trim()) return REQUIRED_FIELDS[2]
    return null
  }

  async function submit(run: boolean) {
    if (busy) return
    setAttempted(true)
    const problem = firstProblem()
    if (problem) {
      // Validation belongs to the submit attempt, not to every keystroke: flag
      // all empty required fields at once, then route the focus to the first one
      // so its error is announced with the field.
      const next: Partial<Record<RequiredField, string>> = {}
      for (const rule of REQUIRED_FIELDS) {
        if (rule.field === 'name' ? !projectName.trim() : rule.field === 'start' ? !start.trim() : !end.trim()) next[rule.field] = rule.message
      }
      setFieldErrors(next)
      setError(problem.message)
      if (problem.field === 'name') nameRef.current?.focus()
      else if (problem.field === 'start') startRef.current?.focus()
      else endRef.current?.focus()
      return
    }
    setFieldErrors({})
    setBusy(true)
    setError('')
    try {
      const created = await api.createProject(
        projectName.trim(),
        start.trim(),
        end.trim(),
        projectDescriptionDraft(constraintsOn, constraints),
      )
      setProjectName('')
      setStart('')
      setEnd('')
      setConstraintsOn(true)
      setConstraints(DEFAULT_PROJECT_CONSTRAINTS)
      onOpenChange(false)
      onCreated?.()
      window.location.assign(
        `/p/${encodeURIComponent(created.id)}/?view=overview${run ? '&autorun=1' : ''}`,
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next) }}>
      <DialogContent className="max-h-[90dvh] max-w-[calc(100%-2rem)] overflow-y-auto overscroll-contain sm:max-w-2xl" aria-busy={busy}>
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>
            项目名称 + 起点 + 终点：起点写现在的授权范围与已知状态（成为项目简报），终点写这次要做到的可判真假状态（成为项目唯一目标）。名称只是显示名：目录使用稳定的短 ID（p-xxxxxxxx），之后改名不会改路径。内置约束文档随项目附带，可以编辑或取消。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <Field
            label="项目名称（必填）"
            hint="控制台与项目列表里的显示名，必填；目录使用与名称无关的稳定短 ID，改名不会改路径。"
          >
            <Input
              ref={nameRef}
              value={projectName}
              onChange={(e) => setProjectName(e.target.value)}
              required
              autoFocus
              aria-label="项目名称"
              aria-invalid={fieldErrors.name ? true : undefined} data-user-invalid={(fieldErrors.name ? true : undefined) ? 'true' : undefined}
              aria-describedby={fieldErrors.name ? 'new-project-name-error' : undefined}
              placeholder="例如：NX 门户外网评估"
            />
            {fieldErrors.name && <span id="new-project-name-error" className="block text-[11px] leading-4 text-destructive">{fieldErrors.name}</span>}
          </Field>

          <Field
            label="起点（必填）"
            hint={fieldErrors.start ? undefined : '授权范围、已知资产、背景与已经确认的部分；这段话会作为项目简报原样交给指挥官与执行者。'}
          >
            <Textarea
              ref={startRef}
              rows={4}
              value={start}
              onChange={(e) => setStart(e.target.value)}
              required
              aria-invalid={fieldErrors.start ? true : undefined} data-user-invalid={(fieldErrors.start ? true : undefined) ? 'true' : undefined}
              aria-describedby={fieldErrors.start ? 'new-project-start-error' : undefined}
              placeholder={
                '例如：评估 example.com 的外网门户，起点是首页和登录口，已知一台测试账号；\n' +
                '只在工作时间测试，不碰与门户无关的资产。'
              }
            />
            {fieldErrors.start && <span id="new-project-start-error" className="block text-[11px] leading-4 text-destructive">{fieldErrors.start}</span>}
          </Field>

          <Field
            label="终点（必填）"
            hint={fieldErrors.end ? undefined : '这次测试要达成的最终状态，写成能判断真假的一句话；不会自动评分，由你（和指挥官）对照事实判断是否达成。'}
          >
            <Textarea
              ref={endRef}
              rows={3}
              value={end}
              onChange={(e) => setEnd(e.target.value)}
              required
              aria-invalid={fieldErrors.end ? true : undefined} data-user-invalid={(fieldErrors.end ? true : undefined) ? 'true' : undefined}
              aria-describedby={fieldErrors.end ? 'new-project-end-error' : undefined}
              placeholder="例如：证明外网门户存在未授权访问或可复现的高危漏洞，并为每条结论留下可复核的原始证据"
            />
            {fieldErrors.end && <span id="new-project-end-error" className="block text-[11px] leading-4 text-destructive">{fieldErrors.end}</span>}
          </Field>

          <div className="rounded-lg border border-hairline bg-surface-2/40">
            <div className="flex items-start gap-3 px-3 py-2.5">
              <Switch
                checked={constraintsOn}
                onCheckedChange={toggleConstraints}
                id="builtin-constraints"
              />
              <label
                htmlFor="builtin-constraints"
                className="min-w-0 flex-1 cursor-pointer text-[12px] leading-relaxed text-ink-2"
              >
                <span className="block font-medium text-ink">内置约束文档（可编辑，可取消）</span>
                约定测试范围、时间和目标数据保护，随项目进入派发与决策。可编辑或关闭；证据与结论的验收要求由账本与控制面规则持续执行。
              </label>
            </div>
            {constraintsOn ? (
              <div className="border-t border-hairline p-3">
                <Textarea
                  rows={10}
                  aria-label="内置约束文档"
                  className="font-mono text-[12px]"
                  value={constraints}
                  onChange={(e) => setConstraints(e.target.value)}
                />
                <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-ink-3">
                  <span className="tw-nums">
                    {constraintsEdited ? '已修改内置默认文本' : '内置默认文本'} · {constraints.trim().length} 字符
                  </span>
                  {constraintsEdited && (
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      onClick={() => setConstraints(DEFAULT_PROJECT_CONSTRAINTS)}
                    >
                      恢复内置默认
                    </Button>
                  )}
                </div>
              </div>
            ) : (
              <div className="border-t border-hairline px-3 py-2 text-[11px] text-ink-3">
                已取消：本次创建不发送约束文档（项目描述为空）。
              </div>
            )}
          </div>

          {error && (
            <div role="alert">
              <Callout tone="destructive">{error}</Callout>
            </div>
          )}
          <Callout tone="info">
            「创建并开始」会立刻让指挥官跑一轮（需要已配置模型）；失败会在概览页提示，不会静默重试。只想先建项目就选「只创建」。
          </Callout>
        </div>
        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            取消
          </Button>
          <Button
            variant="outline"
            onClick={() => void submit(false)}
            disabled={busy}
          >
            只创建
          </Button>
          <Button
            onClick={() => void submit(true)}
            disabled={busy}
            aria-busy={busy}
          >
            {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
            {busy ? '创建中…' : '创建并开始'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
