import type { Event } from './types.ts'

export interface EventGroup { key: string; turn: number; indices: number[]; call?: Event; result?: Event }

/** Match explicit call ids within a turn; legacy results use pending order. */
export function groupRunEvents(events: Event[]): EventGroup[] {
  const groups: EventGroup[]=[]
  let turn=0
  let pending: EventGroup[]=[]
  const byCall=new Map<string,EventGroup>()
  const keys=new Map<string,number>()
  events.forEach((event,index)=>{
    if(event.kind==='turn_started'){turn++;pending=[];byCall.clear()}
    const raw=event.raw as {tool_call?:{id?:string};tool_result?:{call_id?:string}}|undefined
    const callID=event.call_id || raw?.tool_call?.id || raw?.tool_result?.call_id
    if(event.kind==='tool_result'){
      const group=callID ? byCall.get(callID) : pending.find(g=>!g.result&&(!event.tool||g.call?.tool===event.tool))
      if(group&&!group.result){group.indices.push(index);group.result=event;pending=pending.filter(g=>g!==group);return}
    }
    const base=event.event_id || `${event.kind}:${event.turn_id??''}:${callID??''}:${event.at||index}`
    const occurrence=keys.get(base)??0;keys.set(base,occurrence+1)
    const group: EventGroup={key:occurrence?`${base}:${occurrence}`:base,turn,indices:[index]}
    if(event.kind==='tool_call'){group.call=event;pending.push(group);if(callID)byCall.set(callID,group)}
    groups.push(group)
  })
  return groups
}

export function eventArguments(event: Event): unknown {
  const raw=event.raw as {tool_call?:{arguments?:unknown};arguments?:unknown}|undefined
  const argumentsValue=raw?.tool_call?.arguments ?? raw?.arguments
  if(argumentsValue&&typeof argumentsValue==='object'&&'data' in argumentsValue&&typeof argumentsValue.data==='string'){
    try {return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(argumentsValue.data),c=>c.charCodeAt(0))))}catch{/* Preserve undecodable source. */}
  }
  return argumentsValue ?? event.raw
}

export function eventFailed(event: Event): boolean {
  return event.kind==='error'||event.kind==='warning'||event.kind==='tool_result'&&event.ok!==true||event.kind==='turn_ended'&&!!event.stop_reason&&!['completed','done'].includes(event.stop_reason)
}

export function runEventMetrics(events: Event[]) {
  const completed=events.filter(e=>e.kind==='tool_result')
  const timestamps=events.map(e=>Date.parse(e.at)).filter(Number.isFinite)
  const seconds=timestamps.length>1?(Math.max(...timestamps)-Math.min(...timestamps))/1000:0
  return {events:events.length,perMinute:seconds>0?Math.round(events.length*60/seconds):undefined,completed:completed.length,failures:completed.filter(eventFailed).length}
}
