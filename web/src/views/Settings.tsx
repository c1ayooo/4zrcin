import { useCallback, useEffect, useRef, useState } from 'react'
import { Badge, Button, Callout, Field, Input, Spinner } from '@cyber/ui'
import { Page, Panel } from '@/components/Page'
import { ProjectSettingsCard } from '@/components/ProjectSettingsCard'
import { api } from '@/lib/api'
import { useLoad, type AsyncState } from '@/lib/hooks'
import type { RuntimeSettings, SettingsView } from '@/lib/types'

export function SettingsPage() {
 const settings = useLoad(api.settings, [])
 const index = useLoad(api.projectIndex, [])
 const runtime = useLoad(api.runtimeSettings, [])
 const [tab, setTab] = useState('model')
 const [dirtyProjects, setDirtyProjects] = useState<Set<string>>(new Set())
 const onDirty = useCallback((id: string, dirty: boolean) => {setDirtyProjects(current => {const next = new Set(current); if(dirty)next.add(id);else next.delete(id);return next})}, [])
 useEffect(()=>{if(!dirtyProjects.size)return; const prevent=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue=''}; window.addEventListener('beforeunload',prevent); return()=>window.removeEventListener('beforeunload',prevent)},[dirtyProjects])
 return <Page title="设置" subtitle="模型连接与项目运行设置">
  <div className="space-y-4">
   <div className="flex gap-2" role="tablist">
    {[['model','模型连接'],['projects','项目设置']].map(([id,label]) => <Button key={id} role="tab" aria-selected={tab===id} variant={tab===id?'default':'ghost'} size="sm" onClick={()=>setTab(id)}>{label}</Button>)}
   </div>
   <div hidden={tab!=='model'} className="space-y-4">
    <ModelConnection settings={settings} />
    <RuntimeConcurrencyPanel runtime={runtime} />
   </div>
   <div hidden={tab!=='projects'} className="space-y-4">
    {index.error && <Callout tone="destructive">{index.error}</Callout>}
    {index.data?.projects.map(project => <ProjectSettingsCard key={project.id} project={project} defaults={index.data!} onSaved={index.reload} onDirty={onDirty} />)}
   </div>
  </div>
 </Page>
}

type ConnectionDraft = {base_url:string; api_key:string; model:string; provider:string; context_window:string; max_tokens:string}
function connectionDraft(value: SettingsView): ConnectionDraft {
 return {base_url:value.base_url||'', api_key:'', model:value.model||'', provider:value.provider||'openai', context_window:value.context_window ? String(value.context_window) : '', max_tokens:value.max_tokens ? String(value.max_tokens) : ''}
}
function ModelConnection({settings}:{settings:AsyncState<SettingsView>}) {
 const baseline = useRef<ConnectionDraft>()
 const [draft,setDraft] = useState<ConnectionDraft>({base_url:'',api_key:'',model:'',provider:'openai',context_window:'',max_tokens:''})
 const [busy,setBusy] = useState(false)
 const lock=useRef(false)
 const [error,setError] = useState('')
 const [notice,setNotice] = useState('')
 useEffect(()=>{
  if (!settings.data) return
  const next=connectionDraft(settings.data), old=baseline.current
  baseline.current=next
  setDraft(current=>{const merged={...current}; for(const key of Object.keys(next) as (keyof ConnectionDraft)[]) if(!old || current[key]===old[key]) merged[key]=next[key]; return merged})
 },[settings.data])
 const dirty=baseline.current && Object.keys(draft).some(key=>draft[key as keyof ConnectionDraft]!==baseline.current![key as keyof ConnectionDraft])
 useEffect(()=>{if(!dirty)return; const prevent=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue=''}; window.addEventListener('beforeunload',prevent); return()=>window.removeEventListener('beforeunload',prevent)},[dirty])
 const change=(key:keyof ConnectionDraft,value:string)=>setDraft(current=>({...current,[key]:value}))
 async function save(){
  if(lock.current)return
  lock.current=true;setBusy(true);setError('');setNotice('')
  try{
   const body={...draft,context_window:Number(draft.context_window||0),max_tokens:Number(draft.max_tokens||0)}
   if(!Number.isSafeInteger(body.context_window)||!Number.isSafeInteger(body.max_tokens)||body.context_window<0||body.max_tokens<0)throw Error('上下文窗口与输出上限必须是非负整数')
   const next=await api.saveSettings(body)
   baseline.current=connectionDraft(next);setDraft(baseline.current);settings.setData(next)
   setNotice('已保存模型连接。后续会话统一使用此入口，Hub 与 scan 将按新配置启动。')
  }catch(e){setError(e instanceof Error?e.message:String(e))}finally{lock.current=false;setBusy(false)}
 }
 async function check(){
  if(lock.current)return
  lock.current=true;setBusy(true);setError('');setNotice('')
  try{const result=await api.checkModel();if(!result.available)throw Error(result.error||'模型连接失败');setNotice(`连接成功；响应模型：${result.returned_model||'未返回'}。`)}catch(e){setError(e instanceof Error?e.message:String(e))}finally{lock.current=false;setBusy(false)}
 }
 return <Panel title="统一模型连接" bodyClassName="space-y-4">
  <p className="text-sm text-ink-2">指挥官、执行者和复核共用此入口。Key 轮换、模型路由与回退由 Magpie 等外部网关管理；子任务分配和并行协作仍由天吴负责。</p>
  {settings.error && <Callout tone="destructive">{settings.error}</Callout>}
  <fieldset disabled={busy||settings.loading} className="grid gap-4 sm:grid-cols-2">
   <Field label="接口地址"><Input aria-label="接口地址" value={draft.base_url} onChange={e=>change('base_url',e.target.value)} placeholder="http://127.0.0.1:4000/v1" /></Field>
   <Field label="模型名称"><Input aria-label="模型名称" value={draft.model} onChange={e=>change('model',e.target.value)} placeholder="网关暴露的模型名称" /></Field>
   <Field label="API Key" hint={settings.data?.api_key_set?'留空保留已保存的 Key':'填写连接网关所需的单个 Key'}><Input aria-label="API Key" type="password" autoComplete="new-password" value={draft.api_key} onChange={e=>change('api_key',e.target.value)} /></Field>
   <Field label="协议"><Input aria-label="协议" value={draft.provider} onChange={e=>change('provider',e.target.value)} placeholder="openai / anthropic" /></Field>
   <Field label="上下文窗口（可选）"><Input aria-label="上下文窗口" type="number" min={0} value={draft.context_window} onChange={e=>change('context_window',e.target.value)} /></Field>
   <Field label="输出上限（可选）"><Input aria-label="输出上限" type="number" min={0} value={draft.max_tokens} onChange={e=>change('max_tokens',e.target.value)} /></Field>
  </fieldset>
  {error && <Callout tone="destructive">{error}</Callout>}
  {notice && <Callout>{notice}</Callout>}
  <div className="flex flex-wrap gap-2"><Button onClick={()=>void save()} disabled={busy||!dirty}>{busy?<Spinner size="sm"/>:'保存并应用'}</Button><Button variant="outline" onClick={()=>void check()} disabled={busy||!!dirty||!settings.data?.configured}>检测连接</Button></div>
 </Panel>
}

function RuntimeConcurrencyPanel({ runtime }: { runtime: AsyncState<RuntimeSettings> }) {
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    if (runtime.data) setDraft(String(runtime.data.auxiliary_concurrency))
  }, [runtime.data])

  const current = runtime.data?.auxiliary_concurrency
  const max = runtime.data?.max_auxiliary_concurrency ?? 8
  const dirty = runtime.data != null && draft !== String(current)

  async function save() {
    const value = Number(draft)
    if (!Number.isInteger(value) || value < 1 || value > max) {
      setError(`并发必须是 1–${max} 的整数`)
      return
    }
    setBusy(true); setError(''); setNotice('')
    try {
      const next = await api.saveRuntimeSettings(value)
      runtime.setData(next)
      setDraft(String(next.auxiliary_concurrency))
      setNotice('已保存：之后发起的复核/报告按新并发执行，已在跑的不受影响。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel
      title="复核与报告并发"
      extra={<Badge variant="outline" size="sm">实例级</Badge>}
      bodyClassName="space-y-3"
    >
      <div className="grid min-w-0 gap-4 sm:grid-cols-[minmax(0,220px)_1fr] sm:items-end">
        <Field
          label="同时运行的复核/报告数"
          hint={runtime.loading ? '加载中…' : `当前 ${current ?? '…'}，可设 1–${max}`}
        >
          <Input
            type="number"
            min={1}
            max={max}
            step={1}
            value={draft}
            onChange={e => setDraft(e.target.value)}
            disabled={busy || !runtime.data}
            aria-label="复核与报告并发"
          />
        </Field>
        <div className="flex flex-wrap items-center gap-2 pb-0.5">
          <Button size="sm" variant="secondary" onClick={() => void save()} disabled={busy || !runtime.data || !dirty} aria-busy={busy}>
            {busy && <Spinner size="sm" className="h-3.5 w-3.5" />}
            {busy ? '保存中…' : '保存并发'}
          </Button>
          {runtime.error && <Button size="xs" variant="outline" onClick={runtime.reload}>重试读取</Button>}
        </div>
      </div>
      <p className="text-[11px] leading-5 text-ink-3">
        复核与报告共享这道实例级闸门；1 = 串行（默认，最稳）。调大只影响之后的模型调用，已在跑的复核/报告不受影响，也不占执行者并发名额。
      </p>
      <div aria-live="polite" className="space-y-2">
        {notice && <Callout tone="success">{notice}</Callout>}
        {error && <Callout tone="destructive">保存失败：{error}</Callout>}
        {runtime.error && <Callout tone="destructive">读取复核并发失败：{runtime.error}</Callout>}
      </div>
    </Panel>
  )
}
