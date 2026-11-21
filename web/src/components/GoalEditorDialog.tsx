import { useEffect, useRef, useState } from 'react'
import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, Field, Input, Textarea, Callout } from '@cyber/ui'
import { api } from '@/lib/api'
import { documentSaveError } from '@/lib/documents'
import type { Goal } from '@/lib/types'
import { GoalPresets } from './GoalPresets'

/**
 * The fields the editor actually reads. The goals card on the graph page shows
 * the board's compact goal rows, so it must be able to open the editor without
 * inventing the ledger-only fields; a full `Goal` satisfies this shape.
 */
export type EditableGoal = Pick<Goal, 'id' | 'statement'>
  & Partial<Pick<Goal, 'start' | 'closed' | 'closed_reason' | 'instruction_ref' | 'revision'>>

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
  // The statement *is* the `goal` document, so the save has to carry the
  // revision this dialog read. The goal row may already carry it; otherwise it
  // comes from the documents read, which is the same value the 约束 page shows.
  const [revision,setRevision]=useState<number|undefined>(goal.revision)
  const [revisionChecked,setRevisionChecked]=useState(goal.revision!==undefined)
  const statementRef=useRef<HTMLTextAreaElement>(null)
  useEffect(()=>{
    if(goal.revision!==undefined) return
    let alive=true
    api.documents().then(response=>{
      if(!alive) return
      const doc=response?.documents?.goal
      if(doc&&(!doc.goal_id||doc.goal_id===goal.id)) setRevision(doc.revision)
    }).catch(()=>{}).finally(()=>{ if(alive) setRevisionChecked(true) })
    return ()=>{ alive=false }
  },[goal.id,goal.revision])
  async function save(close=false) {
    if (busy) return
    if (!close && !statement.trim()) { statementRef.current?.focus(); return }
    if (!close && revision===undefined) {
      setError('读不到这条目标当前的修订号：刷新页面再改，免得覆盖别人的修改。')
      return
    }
    setBusy(true);setError('')
    try { if(close) await api.closeGoal(goal.id,reason);else await api.updateGoal(goal.id,statement,start,revision as number);onSaved();onClose() }
    catch(e){setError(documentSaveError(e))} finally {setBusy(false)}
  }
  const revisionMissing = !goal.closed && revisionChecked && revision===undefined
  return <Dialog open onOpenChange={open=>{if(!open&&!busy)onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto overscroll-contain" aria-busy={busy}>
    <DialogHeader><DialogTitle>管理目标</DialogTitle><DialogDescription>修改验收定义会撤回当前达成收据，历史版本保留。关闭表示撤回目标，不代表达成。</DialogDescription></DialogHeader>
    {error&&<div role="alert"><Callout tone="destructive">{error}</Callout></div>}
    {!goal.closed&&<GoalPresets disabled={busy} onPick={preset=>{setStart(preset.start);setStatement(preset.statement)}} />}
    <Field label="起点"><Input disabled={busy||goal.closed} enterKeyHint="next" value={start} onChange={e=>setStart(e.target.value)} /></Field>
    <Field label="终点"><Textarea ref={statementRef} disabled={busy||goal.closed} required autoFocus aria-invalid={!statement.trim() ? true : undefined} data-user-invalid={(!statement.trim() ? true : undefined) ? 'true' : undefined} value={statement} onChange={e=>setStatement(e.target.value)} /></Field>
    {goal.instruction_ref&&<p className="text-xs text-ink-3">指令出处：{goal.instruction_ref}</p>}
    {revisionMissing&&<p role="alert" className="text-[11px] leading-4 text-destructive">读不到这条目标当前的修订号（可能已被改过）：刷新页面再改，免得覆盖别人的修改。</p>}
    {goal.closed?<p className="text-sm text-ink-3">已关闭：{goal.closed_reason}</p>:<>
      <Button disabled={busy||!statement.trim()||revision===undefined} onClick={()=>void save()}>保存修改</Button>
      <Field label="关闭原因"><Input disabled={busy} value={reason} onChange={e=>setReason(e.target.value)} placeholder="说明为什么撤回这个目标" /></Field>
      <Button variant="outline" disabled={busy||!reason.trim()} onClick={()=>void save(true)}>关闭目标</Button>
    </>}
  </DialogContent></Dialog>
}
