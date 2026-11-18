import { useRef, useState } from 'react'
import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, Field, Input, Textarea, Callout } from '@cyber/ui'
import { api } from '@/lib/api'
import type { Goal } from '@/lib/types'
import { GoalPresets } from './GoalPresets'

/**
 * The fields the editor actually reads. The goals card on the graph page shows
 * the board's compact goal rows, so it must be able to open the editor without
 * inventing the ledger-only fields; a full `Goal` satisfies this shape.
 */
export type EditableGoal = Pick<Goal, 'id' | 'statement'>
  & Partial<Pick<Goal, 'start' | 'closed' | 'closed_reason' | 'instruction_ref'>>

export function GoalEditorDialog({ goal, onClose, onSaved }: {goal: EditableGoal | null; onClose: () => void; onSaved: () => void}) {
  if (!goal) return null
  return <Editor key={goal.id} goal={goal} onClose={onClose} onSaved={onSaved} />
}
function Editor({goal,onClose,onSaved}:{goal:EditableGoal;onClose:()=>void;onSaved:()=>void}) {
  const [statement,setStatement]=useState(goal.statement)
  const [start,setStart]=useState(goal.start ?? '')
  const [reason,setReason]=useState('')
  const [error,setError]=useState('')
  const [busy,setBusy]=useState(false)
  const statementRef=useRef<HTMLTextAreaElement>(null)
  async function save(close=false) {
    if (busy) return
    if (!close && !statement.trim()) { statementRef.current?.focus(); return }
    setBusy(true);setError('')
    try { if(close) await api.closeGoal(goal.id,reason);else await api.updateGoal(goal.id,statement,start);onSaved();onClose() }
    catch(e){setError(e instanceof Error? e.message:String(e))} finally {setBusy(false)}
  }
  return <Dialog open onOpenChange={open=>{if(!open&&!busy)onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto overscroll-contain" aria-busy={busy}>
    <DialogHeader><DialogTitle>管理目标</DialogTitle><DialogDescription>修改验收定义会撤回当前达成收据，历史版本保留。关闭表示撤回目标，不代表达成。</DialogDescription></DialogHeader>
    {error&&<div role="alert"><Callout tone="destructive">{error}</Callout></div>}
    {!goal.closed&&<GoalPresets disabled={busy} onPick={preset=>{setStart(preset.start);setStatement(preset.statement)}} />}
    <Field label="起点"><Input disabled={busy||goal.closed} enterKeyHint="next" value={start} onChange={e=>setStart(e.target.value)} /></Field>
    <Field label="终点"><Textarea ref={statementRef} disabled={busy||goal.closed} required autoFocus aria-invalid={!statement.trim() ? true : undefined} data-user-invalid={(!statement.trim() ? true : undefined) ? 'true' : undefined} value={statement} onChange={e=>setStatement(e.target.value)} /></Field>
    {goal.instruction_ref&&<p className="text-xs text-ink-3">指令出处：{goal.instruction_ref}</p>}
    {goal.closed?<p className="text-sm text-ink-3">已关闭：{goal.closed_reason}</p>:<>
      <Button disabled={busy||!statement.trim()} onClick={()=>void save()}>保存修改</Button>
      <Field label="关闭原因"><Input disabled={busy} value={reason} onChange={e=>setReason(e.target.value)} placeholder="说明为什么撤回这个目标" /></Field>
      <Button variant="outline" disabled={busy||!reason.trim()} onClick={()=>void save(true)}>关闭目标</Button>
    </>}
  </DialogContent></Dialog>
}
