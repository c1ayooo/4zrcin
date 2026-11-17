import { test } from 'node:test'
import assert from 'node:assert/strict'
import { groupRunEvents, eventArguments, runEventMetrics } from '../src/lib/run-events.ts'
import type { Event } from '../src/lib/types.ts'

test('tool results pair by call id and cannot leak across turns',()=>{
 const events:Event[]=[
  {kind:'turn_started',at:'2026-01-01T00:00:00Z'},
  {kind:'tool_call',tool:'read',call_id:'a',at:'2026-01-01T00:00:01Z'},
  {kind:'tool_call',tool:'bash',call_id:'b',at:'2026-01-01T00:00:02Z'},
  {kind:'tool_result',tool:'bash',call_id:'b',ok:false,at:'2026-01-01T00:00:03Z'},
  {kind:'tool_result',tool:'read',call_id:'a',ok:true,at:'2026-01-01T00:00:04Z'},
  {kind:'turn_started',at:'2026-01-01T00:00:05Z'},
  {kind:'tool_result',tool:'read',call_id:'a',ok:true,at:'2026-01-01T00:00:06Z'},
 ]
 const groups=groupRunEvents(events)
 assert.deepEqual(groups[1].indices,[1,4]);assert.deepEqual(groups[2].indices,[2,3])
 assert.equal(groups[4].turn,2);assert.deepEqual(groups[4].indices,[6])
 assert.equal(runEventMetrics(events).failures,1)
})

test('raw base64 JSON arguments retain Unicode and malformed payloads remain visible',()=>{
 const value={command:'echo 测试'}
 const raw={tool_call:{arguments:{data:Buffer.from(JSON.stringify(value)).toString('base64')}}}
 assert.deepEqual(eventArguments({kind:'tool_call',at:'',raw}),value)
 assert.deepEqual(eventArguments({kind:'tool_call',at:'',raw:{arguments:{data:'broken!'}}}),{data:'broken!'})
})

test('historical native envelopes retain call pairing and repeated timestamps have distinct keys',()=>{
 const events:Event[]=[
  {kind:'tool_call',at:'2026-01-01T00:00:01Z',tool:'bash',raw:{tool_call:{id:'a'}}},
  {kind:'tool_call',at:'2026-01-01T00:00:01Z',tool:'bash',raw:{tool_call:{id:'b'}}},
  {kind:'tool_result',at:'2026-01-01T00:00:02Z',raw:{tool_result:{call_id:'b'}}},
  {kind:'tool_result',at:'2026-01-01T00:00:03Z',raw:{tool_result:{call_id:'a'}}},
  {kind:'status',at:'2026-01-01T00:00:04Z'},
  {kind:'status',at:'2026-01-01T00:00:04Z'},
 ]
 const groups=groupRunEvents(events)
 assert.deepEqual(groups[0].indices,[0,3]);assert.deepEqual(groups[1].indices,[1,2])
 assert.equal(new Set(groups.map(g=>g.key)).size,groups.length)
})
