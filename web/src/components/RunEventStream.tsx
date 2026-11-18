import { runStreamViewport } from '@/lib/run-stream'
import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Badge, Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@cyber/ui'
import { eventArguments, eventFailed, groupRunEvents, runEventMetrics, type EventGroup } from '@/lib/run-events'
import { useScrollMemoryOn, readViewMemory, writeViewMemory } from '@/lib/view-memory'
import { fmtTime } from '@/lib/format'
import type { Event } from '@/lib/types'

/**
 * One collapsible event group.
 *
 * Extracted and memoised because the stream is the highest-churn list on the
 * page: polling appends events while the operator toggles filters and copies
 * JSON, and every one of those state flips used to rebuild all ~400 groups.
 * With this boundary only the groups whose `open`/`focus`/`copied` actually
 * changed re-render.
 *
 * No `content-visibility: auto` here on purpose: the scroll position of this
 * panel is remembered and restored by `useScrollMemoryOn`, and the runs page
 * calls `scrollIntoView` on a `[data-event-index]` target. Both depend on the
 * real box of every group, which deferred rows would not have.
 */
interface GroupCardProps {
  group: EventGroup
  events: Event[]
  open: boolean
  focused: boolean
  copied: string
  onToggle: (key: string) => void
  onCopy: (key: string, value: unknown) => void
}

const GroupCard = memo(function GroupCard({ group, events, open, focused, copied, onToggle, onCopy }: GroupCardProps) {
  const first = events[group.indices[0]]
  const failed = group.indices.some((i) => eventFailed(events[i]))
  return (
    <article
      role="listitem"
      className={`rounded-lg border p-2 ${failed ? 'border-warning/40' : 'border-hairline'} ${focused ? 'bg-primary/10' : ''}`}
    >
      <button
        className="flex w-full items-start gap-2 text-left text-xs"
        aria-expanded={open}
        onClick={() => onToggle(group.key)}
      >
        <span className="text-ink-3" aria-hidden="true">{open ? '▾' : '▸'}</span>
        <Badge size="sm" variant={failed ? 'warning' : 'muted'}>{first.kind}</Badge>
        <span className="font-mono text-info">{first.tool || first.role || `回合 ${group.turn || 1}`}</span>
        <span className="min-w-0 flex-1 truncate text-ink-2">{first.text?.slice(0, 160)}</span>
        {group.result?.duration_ms !== undefined && <span>{(group.result.duration_ms / 1000).toFixed(2)}s</span>}
        <span className="shrink-0 text-ink-3">{fmtTime(first.at)}</span>
      </button>
      {group.indices.map((index) => {
        const event = events[index]
        return (
          <div
            key={index}
            data-event-index={index}
            className={open ? 'mt-2 border-t border-hairline pt-2' : 'h-0 overflow-hidden'}
          >
            {open && (
              <>
                <div className="mb-1 flex items-center gap-2 text-xs text-ink-3">
                  <span>{event.kind} {event.call_id}</span>
                  <Button size="xs" variant="ghost" onClick={() => onCopy(String(index), event)}>
                    {copied === String(index) ? '已复制' : '复制 JSON'}
                  </Button>
                </div>
                <pre className="tw-break whitespace-pre-wrap text-[12px] leading-relaxed text-ink-2">{event.text || ''}</pre>
                {event.raw !== undefined && (
                  <>
                    <div className="mt-2 text-xs text-ink-3">
                      {event.kind === 'tool_call' ? '工具参数（解码）' : '原始事件 / 返回'}
                    </div>
                    <pre className="tw-break whitespace-pre-wrap text-[11px] text-ink-3">
                      {JSON.stringify(event.kind === 'tool_call' ? eventArguments(event) : event.raw, null, 2)}
                    </pre>
                  </>
                )}
              </>
            )}
          </div>
        )
      })}
    </article>
  )
})

export function RunEventStream({ events, runID, focusIndex }: { events: Event[]; runID: string; focusIndex: number | null }) {
  const scope = `run-stream:${window.location.pathname}:${runID}`
  const [expanded, setExpanded] = useState<string[]>(() => readViewMemory<string[]>(scope, 'expanded') ?? [])
  const [filter, setFilter] = useState(() => readViewMemory<string>(scope, 'filter') ?? 'all')
  const [copied, setCopied] = useState('')
  const [error, setError] = useState('')
  const root = useScrollMemoryOn<HTMLDivElement>(scope, runStreamViewport)
  const groups = useMemo(() => groupRunEvents(events), [events])
  const metrics = useMemo(() => runEventMetrics(events), [events])
  const expand = useCallback((next: string[]) => {
    setExpanded(next)
    writeViewMemory(scope, 'expanded', next)
  }, [scope])
  useEffect(() => {
    if (focusIndex === null) return
    const group = groups.find((g) => g.indices.includes(focusIndex))
    if (group && !expanded.includes(group.key)) expand([...expanded, group.key])
  }, [focusIndex, groups])
  const copy = useCallback(async (key: string, value: unknown) => {
    try {
      await navigator.clipboard.writeText(typeof value === 'string' ? value : JSON.stringify(value, null, 2))
      setCopied(key)
      setError('')
    } catch {
      setError('复制失败：剪贴板不可用')
    }
  }, [])
  const toggle = useCallback((key: string) => {
    setExpanded((current) => {
      const next = current.includes(key) ? current.filter((k) => k !== key) : [...current, key]
      writeViewMemory(scope, 'expanded', next)
      return next
    })
  }, [scope])
  const visibleGroups = useMemo(
    () =>
      groups.filter(
        (group) =>
          filter === 'all' ||
          group.indices.includes(focusIndex ?? -1) ||
          group.indices.some((index) => {
            const e = events[index]
            return filter === 'errors'
              ? eventFailed(e)
              : filter === 'input'
                ? ['input', 'prompt', 'injected'].includes(e.kind)
                : e.kind === 'injected' || e.state?.includes('compact') || e.kind === 'state'
          }),
      ),
    [groups, events, filter, focusIndex],
  )
  return (
    <div ref={root} className="space-y-2">
      <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-hairline bg-card p-2 text-xs text-ink-3">
        <span>已载入 {metrics.events} 个事件（最近 400 条及本次输入） · {metrics.perMinute ?? '—'}/分钟 · 工具失败 {metrics.failures}/{metrics.completed}</span>
        <Button size="xs" variant="ghost" onClick={() => expand(groups.map((g) => g.key))}>全部展开</Button>
        <Button size="xs" variant="ghost" onClick={() => expand([])}>全部收起</Button>
        <Button size="xs" variant="ghost" onClick={() => void copy('all', events)}>{copied === 'all' ? '已复制' : '复制原始 JSON'}</Button>
        <label className="sr-only" htmlFor={`${scope}-filter`}>事件筛选</label>
        <Select value={filter} onValueChange={(value) => { setFilter(value); writeViewMemory(scope, 'filter', value) }}>
          <SelectTrigger id={`${scope}-filter`} className="h-7 w-28"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部事件</SelectItem>
            <SelectItem value="errors">仅异常</SelectItem>
            <SelectItem value="handoff">交接与压缩</SelectItem>
            <SelectItem value="input">输入提示词</SelectItem>
          </SelectContent>
        </Select>
        <span role="status" aria-live="polite" className="sr-only">
          显示 {visibleGroups.length} / {groups.length} 个事件组
        </span>
      </div>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
      {groups.length === 0 && (
        <p className="px-1 py-6 text-center text-xs text-ink-3">这一条 run 还没有事件。</p>
      )}
      {groups.length > 0 && visibleGroups.length === 0 && (
        <p className="px-1 py-6 text-center text-xs text-ink-3">当前筛选下没有事件。</p>
      )}
      <div role="list" aria-label="运行事件组" className="space-y-2">
        {visibleGroups.map((group) => (
          <GroupCard
            key={group.key}
            group={group}
            events={events}
            open={expanded.includes(group.key)}
            focused={group.indices.includes(focusIndex ?? -1)}
            copied={copied}
            onToggle={toggle}
            onCopy={copy}
          />
        ))}
      </div>
    </div>
  )
}
