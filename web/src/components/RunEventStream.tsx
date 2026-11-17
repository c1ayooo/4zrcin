import { runStreamViewport } from '@/lib/run-stream'
import { useEffect, useMemo, useState } from 'react'
import { Badge, Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@cyber/ui'
import { eventArguments, eventFailed, groupRunEvents, runEventMetrics } from '@/lib/run-events'
import { useScrollMemoryOn, readViewMemory, writeViewMemory } from '@/lib/view-memory'
import { fmtTime } from '@/lib/format'
import type { Event } from '@/lib/types'

export function RunEventStream({events,runID,focusIndex}:{events:Event[];runID:string;focusIndex:number|null}) {
  const scope=`run-stream:${window.location.pathname}:${runID}`
  const [expanded,setExpanded]=useState<string[]>(()=>readViewMemory<string[]>(scope,'expanded')??[])
  const [filter,setFilter]=useState(()=>readViewMemory<string>(scope,'filter')??'all')
  const [copied,setCopied]=useState('')
  const [error,setError]=useState('')
  const root=useScrollMemoryOn<HTMLDivElement>(scope,runStreamViewport)
  const groups=useMemo(()=>groupRunEvents(events),[events])
  const metrics=useMemo(()=>runEventMetrics(events),[events])
  function expand(next:string[]){setExpanded(next);writeViewMemory(scope,'expanded',next)}
  useEffect(()=>{
    if(focusIndex===null)return
    const group=groups.find(g=>g.indices.includes(focusIndex))
    if(group&&!expanded.includes(group.key))expand([...expanded,group.key])
  },[focusIndex,groups])
  async function copy(key:string,value:unknown){try{await navigator.clipboard.writeText(typeof value==='string'?value:JSON.stringify(value,null,2));setCopied(key);setError('')}catch{setError('复制失败：剪贴板不可用')}}
  return <div ref={root} className="space-y-2">
    <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-hairline bg-card p-2 text-xs text-ink-3">
      <span>已载入 {metrics.events} 个事件（最近 400 条及本次输入） · {metrics.perMinute ?? '—'}/分钟 · 工具失败 {metrics.failures}/{metrics.completed}</span>
      <Button size="xs" variant="ghost" onClick={()=>expand(groups.map(g=>g.key))}>全部展开</Button>
      <Button size="xs" variant="ghost" onClick={()=>expand([])}>全部收起</Button>
      <Button size="xs" variant="ghost" onClick={()=>void copy('all',events)}>{copied==='all'?'已复制':'复制原始 JSON'}</Button>
      <Select value={filter} onValueChange={value=>{setFilter(value);writeViewMemory(scope,'filter',value)}}><SelectTrigger className="h-7 w-28"><SelectValue/></SelectTrigger><SelectContent>
        <SelectItem value="all">全部事件</SelectItem><SelectItem value="errors">仅异常</SelectItem><SelectItem value="handoff">交接与压缩</SelectItem><SelectItem value="input">输入提示词</SelectItem>
      </SelectContent></Select>
    </div>
    {error&&<p className="text-xs text-destructive">{error}</p>}
    {groups.filter(group=>filter==='all'||group.indices.includes(focusIndex??-1)||group.indices.some(index=>{
      const e=events[index];return filter==='errors'?eventFailed(e):filter==='input'?['input','prompt','injected'].includes(e.kind):e.kind==='injected'||e.state?.includes('compact')||e.kind==='state'
    })).map(group=>{
      const first=events[group.indices[0]],open=expanded.includes(group.key)
      const failed=group.indices.some(i=>eventFailed(events[i]))
      return <article key={group.key} className={`rounded-lg border p-2 ${failed?'border-warning/40':'border-hairline'} ${group.indices.includes(focusIndex??-1)?'bg-primary/10':''}`}>
        <button className="flex w-full items-start gap-2 text-left text-xs" aria-expanded={open} onClick={()=>expand(open?expanded.filter(k=>k!==group.key):[...expanded,group.key])}>
          <span className="text-ink-3">{open?'▾':'▸'}</span><Badge size="sm" variant={failed?'warning':'muted'}>{first.kind}</Badge>
          <span className="font-mono text-info">{first.tool||first.role||`回合 ${group.turn||1}`}</span>
          <span className="min-w-0 flex-1 truncate text-ink-2">{first.text?.slice(0,160)}</span>
          {group.result?.duration_ms!==undefined&&<span>{(group.result.duration_ms/1000).toFixed(2)}s</span>}
          <span className="shrink-0 text-ink-3">{fmtTime(first.at)}</span>
        </button>
        {group.indices.map(index=>{
          const event=events[index]
          return <div key={index} data-event-index={index} className={open?'mt-2 border-t border-hairline pt-2':'h-0 overflow-hidden'}>
            {open&&<><div className="mb-1 flex items-center gap-2 text-xs text-ink-3"><span>{event.kind} {event.call_id}</span><Button size="xs" variant="ghost" onClick={()=>void copy(String(index),event)}>{copied===String(index)?'已复制':'复制 JSON'}</Button></div>
              <pre className="tw-break whitespace-pre-wrap text-[12px] leading-relaxed text-ink-2">{event.text||''}</pre>
              {event.raw!==undefined&&<><div className="mt-2 text-xs text-ink-3">{event.kind==='tool_call'?'工具参数（解码）':'原始事件 / 返回'}</div><pre className="tw-break whitespace-pre-wrap text-[11px] text-ink-3">{JSON.stringify(event.kind==='tool_call'?eventArguments(event):event.raw,null,2)}</pre></>}
            </>}
          </div>
        })}
      </article>
    })}
  </div>
}
