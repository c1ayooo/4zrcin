import { useEffect, useRef, useState } from 'react'
import {
  Badge, Button, Callout, Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle, Field, Input, Select, SelectContent, SelectItem,
  SelectTrigger, SelectValue, Spinner, Switch,
} from '@cyber/ui'
import { Panel } from './Page'
import { api } from '@/lib/api'
import {
  SCOPE_RULE_ACTIONS, SCOPE_RULE_MAX_ROWS,
  scopeRulesDraftFromProject, scopeRulesPayload, scopeRulesProblems, scopeRulesSummary,
} from '@/lib/settings'
import type { ScopeRuleAction, ScopeRuleDraft, ScopeRulesDraft } from '@/lib/settings'
import type { ProjectInfo } from '@/lib/types'

const ACTION_LABELS: Record<ScopeRuleAction, string> = {
  record: '记录放行',
  review: '挂起待审',
  block: '拒绝',
}

const ACTION_HINTS: Record<ScopeRuleAction, string> = {
  record: '放行并记录',
  review: '挂起等待你在收件箱批准或拒绝',
  block: '直接拒绝该请求',
}

function emptyRow(): ScopeRuleDraft {
  return { action: 'record', method: '*', host: '', path_prefix: '' }
}

/** True when the draft would refuse requests by itself (a row or the default). */
function usesBlockAction(draft: ScopeRulesDraft): boolean {
  return draft.default_action === 'block' || draft.rules.some(rule => rule.action === 'block')
}

/**
 * The project's machine-matchable test range. Rows are first-hit-wins; an
 * unmatched request falls to the default action. The enabled switch and the
 * table save together: a disabled set is a draft the operator keeps, still
 * validated, so flipping the switch later cannot arm rules the engine would
 * reject. Enabling is confirmed first — it changes what happens to live
 * traffic (holds appear in the inbox), not just a stored preference.
 */
export function ScopeRulesCard({ project, onSaved, onDirty }: {
  project: ProjectInfo
  onSaved: () => void
  onDirty: (id: string, dirty: boolean) => void
}) {
  const baseline = useRef(scopeRulesDraftFromProject(project))
  const [draft, setDraft] = useState(baseline.current)
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [rowErrors, setRowErrors] = useState<(string | null)[]>([])
  const [confirmEnable, setConfirmEnable] = useState(false)
  // Saving a set that contains a block action needs an explicit second
  // confirmation (same care the guardrail on_error=block takes): a mis-saved
  // block rule silently refuses real requests, which is a different failure
  // class than a missed hold. Required on every save that carries block.
  const [confirmBlock, setConfirmBlock] = useState<ScopeRulesDraft | null>(null)
  const tableDirty = JSON.stringify(draft) !== JSON.stringify(baseline.current)

  useEffect(() => {
    const incoming = scopeRulesDraftFromProject(project)
    const previous = baseline.current
    baseline.current = incoming
    setDraft(current => {
      if (JSON.stringify(current) !== JSON.stringify(previous)) return current
      return incoming
    })
  }, [project])
  useEffect(() => { onDirty(`${project.id}:scope_rules`, tableDirty) }, [project.id, tableDirty, onDirty])
  useEffect(() => () => onDirty(`${project.id}:scope_rules`, false), [project.id, onDirty])

  function update(mutate: (draft: ScopeRulesDraft) => ScopeRulesDraft) {
    setDraft(current => mutate(current))
    setRowErrors([])
    setError('')
    setNote('')
  }

  async function save(next?: Partial<ScopeRulesDraft>) {
    if (lock.current) return
    const target = next ? { ...draft, ...next } : draft
    const problems = scopeRulesProblems(target)
    if (problems) {
      setRowErrors(problems.rows)
      setError(problems.default_action ?? '规则表里有未通过校验的行，已标出第一处问题。')
      setNote('')
      return
    }
    if (usesBlockAction(target)) {
      setConfirmBlock(target)
      return
    }
    await performSave(target)
  }

  async function performSave(target: ScopeRulesDraft) {
    if (lock.current) return
    lock.current = true
    setBusy(true); setError(''); setNote('')
    try {
      const saved = await api.updateProject(project.id, { scope_rules: scopeRulesPayload(target) })
      baseline.current = scopeRulesDraftFromProject(saved)
      setDraft(baseline.current)
      setNote(saved.scope_rules?.enabled
        ? '已保存并启用。规则随每个执行回合下发，保存后从下一回合生效；命中「挂起待审」的请求会出现在待办收件箱。'
        : '已保存为草稿。未启用时管线只记录不拦，关闭也会随下一回合同步到执行端。')
      onSaved()
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { lock.current = false; setBusy(false) }
  }

  function onEnableSwitch(next: boolean) {
    if (next) setConfirmEnable(true)
    else void save({ enabled: false })
  }

  return <Panel
    title="测试范围"
    extra={
      <div className="flex flex-wrap items-center justify-end gap-1.5">
        <Badge variant={draft.enabled ? 'secondary' : 'muted'} size="sm">{scopeRulesSummary(project.scope_rules)}</Badge>
        {tableDirty && <Badge variant="warning" size="sm">未保存</Badge>}
      </div>
    }
    bodyClassName="@container space-y-4"
  >
    <div className="space-y-1 text-[12px] leading-relaxed text-ink-2">
      <p>
        逐条匹配经过审计管线的请求与命令（自上而下首条命中）：命中的行决定处置，未命中的按「默认动作」处理。
        规则表是你在控制台显式声明的检查范围，不校验、不替代项目文档里的授权边界原文。
      </p>
    </div>
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-hairline bg-surface-2/40 px-3 py-2.5">
      <div className="flex items-center gap-3">
        <Switch
          id={`scope-enabled-${project.id}`}
          checked={draft.enabled}
          disabled={busy}
          onCheckedChange={onEnableSwitch}
        />
        <label htmlFor={`scope-enabled-${project.id}`} className="cursor-pointer text-[12px] font-medium text-ink">
          启用范围检查 · {draft.enabled ? '开启' : '关闭'}
        </label>
      </div>
      <div className="flex items-center gap-3">
        <Switch
          id={`scope-shadow-${project.id}`}
          checked={draft.shadow}
          disabled={busy}
          onCheckedChange={next => update(current => ({ ...current, shadow: next }))}
        />
        <label htmlFor={`scope-shadow-${project.id}`} className="cursor-pointer text-[12px] text-ink-2">
          影子模式（只判定记录，始终放行）
        </label>
      </div>
      <Field label="默认动作" className="min-w-40">
        <Select
          value={draft.default_action}
          disabled={busy}
          onValueChange={next => update(current => ({ ...current, default_action: next as ScopeRuleAction }))}
        >
          <SelectTrigger aria-label="默认动作"><SelectValue /></SelectTrigger>
          <SelectContent>
            {SCOPE_RULE_ACTIONS.map(action => (
              <SelectItem key={action} value={action}>{ACTION_LABELS[action]}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    </div>
    {draft.rules.length === 0 && (
      <div className="rounded-lg border border-hairline px-3 py-2 text-[12px] text-ink-3">
        暂无规则：全部请求都按默认动作处理。添加一条「{ACTION_LABELS.record} · * · *.example.com · /」可以把已知目标标成放行，
        再用默认动作兜住其余请求。
      </div>
    )}
    {draft.rules.length > 0 && (
      <div className="space-y-2">
        <div className="grid grid-cols-[minmax(0,7rem)_minmax(0,8rem)_minmax(0,1fr)_minmax(0,1fr)_2rem] items-start gap-2 px-1 text-[11px] font-medium text-ink-3">
          <span>动作</span><span>方法</span><span>主机</span><span>路径前缀</span><span />
        </div>
        {draft.rules.map((rule, index) => {
          const rowError = rowErrors[index] ?? null
          return <div key={index} className="grid grid-cols-[minmax(0,7rem)_minmax(0,8rem)_minmax(0,1fr)_minmax(0,1fr)_2rem] items-start gap-2">
            <Select value={rule.action} disabled={busy}
              onValueChange={next => update(current => {
                const rules = [...current.rules]
                rules[index] = { ...rules[index], action: next as ScopeRuleAction }
                return { ...current, rules }
              })}>
              <SelectTrigger aria-label={`规则 ${index + 1} 动作`}><SelectValue /></SelectTrigger>
              <SelectContent>
                {SCOPE_RULE_ACTIONS.map(action => (
                  <SelectItem key={action} value={action}>{ACTION_LABELS[action]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input aria-label={`规则 ${index + 1} 方法`} spellCheck={false} placeholder="*" value={rule.method}
              aria-invalid={rowError != null ? true : undefined}
              onChange={e => update(current => {
                const rules = [...current.rules]
                rules[index] = { ...rules[index], method: e.target.value }
                return { ...current, rules }
              })} />
            <Input aria-label={`规则 ${index + 1} 主机`} spellCheck={false} autoCapitalize="none" placeholder="*.example.com" value={rule.host}
              aria-invalid={rowError != null ? true : undefined}
              onChange={e => update(current => {
                const rules = [...current.rules]
                rules[index] = { ...rules[index], host: e.target.value }
                return { ...current, rules }
              })} />
            <Input aria-label={`规则 ${index + 1} 路径前缀`} spellCheck={false} autoCapitalize="none" placeholder="/（全部路径）" value={rule.path_prefix}
              aria-invalid={rowError != null ? true : undefined}
              onChange={e => update(current => {
                const rules = [...current.rules]
                rules[index] = { ...rules[index], path_prefix: e.target.value }
                return { ...current, rules }
              })} />
            <Button size="xs" variant="ghost" disabled={busy} aria-label={`删除规则 ${index + 1}`}
              onClick={() => update(current => ({ ...current, rules: current.rules.filter((_, i) => i !== index) }))}>
              ×
            </Button>
            {rowError && <div className="col-span-5 text-[11px] text-warning">{rowError}</div>}
          </div>
        })}
      </div>
    )}
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" variant="soft" disabled={busy || draft.rules.length >= SCOPE_RULE_MAX_ROWS}
        onClick={() => update(current => ({ ...current, rules: [...current.rules, emptyRow()] }))}>
        添加规则
      </Button>
      <span className="text-[11px] text-ink-3">{draft.rules.length}/{SCOPE_RULE_MAX_ROWS} 条 · 自上而下首条命中</span>
    </div>
    <div className="flex flex-wrap items-center gap-2 border-t border-hairline pt-4">
      <Button size="sm" variant="soft" onClick={() => void save()} disabled={busy || !tableDirty} aria-busy={busy}>
        {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
        {busy ? '保存中…' : '保存测试范围'}
      </Button>
      <Button size="sm" variant="ghost" disabled={busy || !tableDirty}
        onClick={() => { setDraft(baseline.current); setRowErrors([]); setError(''); setNote('') }}>
        撤销改动
      </Button>
    </div>
    <div aria-live="polite" className="space-y-2">
      {note && <Callout tone="success">{note}</Callout>}
      {error && <Callout tone="destructive">保存失败：{error}</Callout>}
    </div>
    <Dialog open={confirmEnable} onOpenChange={next => { if (!busy) setConfirmEnable(next) }}>
      <DialogContent className="max-h-[90dvh] max-w-[calc(100%-2rem)] overflow-y-auto overscroll-contain sm:max-w-md" aria-busy={busy}>
        <DialogHeader>
          <DialogTitle>启用范围检查？</DialogTitle>
          <DialogDescription>
            「{project.name}」的流量与命令将逐条匹配这张规则表，之后规则不会自动放宽或收紧。
          </DialogDescription>
        </DialogHeader>
        <Callout tone="warning">
          <div className="space-y-1 text-[12px] leading-relaxed">
            <p>命中「挂起待审」的请求会停下来等你在收件箱批准或拒绝；未命中的按默认动作「{ACTION_LABELS[draft.default_action]}」处理（{ACTION_HINTS[draft.default_action]}）。</p>
            <p>影子模式只判定记录、始终放行，适合先试跑一张新表。</p>
          </div>
        </Callout>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => setConfirmEnable(false)}>再看看</Button>
          <Button variant="default" disabled={busy}
            onClick={() => { setConfirmEnable(false); void save({ enabled: true }) }}>
            {busy ? '保存中…' : '启用并保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <Dialog open={confirmBlock != null} onOpenChange={next => { if (!busy && !next) setConfirmBlock(null) }}>
      <DialogContent className="max-h-[90dvh] max-w-[calc(100%-2rem)] overflow-y-auto overscroll-contain sm:max-w-md" aria-busy={busy}>
        <DialogHeader>
          <DialogTitle>保存含「拒绝」动作的规则？</DialogTitle>
          <DialogDescription>
            这张规则表包含 block 动作：命中的请求会被直接拒绝，不会进入收件箱，也不会再问你。
          </DialogDescription>
        </DialogHeader>
        <Callout tone="warning">
          <div className="space-y-1 text-[12px] leading-relaxed">
            <p>与「挂起待审」不同，block 不留挽回机会：配置写错（比如主机写得太宽或动作选错）会静默拒绝真实请求。</p>
            <p>建议先用影子模式试跑，确认命中符合预期后再正式启用 block。</p>
          </div>
        </Callout>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => setConfirmBlock(null)}>再看看</Button>
          <Button variant="destructive" disabled={busy}
            onClick={() => { const target = confirmBlock; setConfirmBlock(null); if (target) void performSave(target) }}>
            {busy ? '保存中…' : '确认保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </Panel>
}
