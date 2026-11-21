// The operator's one 下发 box: one sentence plus two explicit choices (时效 and
// 对象). The server maps the choices onto three primitives so the operator does
// not have to know them:
//   一次性 + 指挥官      -> durable one-shot message to the commander (wakes a round)
//   一次性 + 方向        -> durable one-shot message to that direction only
//   一次性 + 方向 + steer -> stop its live turn and continue with the note
//   持续   + 整个项目    -> project-scope instruction (all future dispatches)
//   持续   + 方向        -> instruction scoped to that direction subtree
// A one-shot hint is never an instruction and a persistent constraint is never a
// message; the receipt says which one happened and what it affects.
//
// 「持续」 is a shortcut into the same rows the 持久指令 page manages, not a
// second kind of record: while it is selected the box echoes the standing set,
// because an older sentence about the same topic must be visible at the moment
// one more is written.
import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import {
  Button,
  Checkbox,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Spinner,
  Textarea,
  ToggleGroup,
  ToggleGroupItem,
} from '@cyber/ui'
import { readViewMemory, writeViewMemory } from '@/lib/view-memory'
import { useSessionState } from '@/lib/session-state'
import { api } from '@/lib/api'
import {
  canSteer,
  directiveErrorMessage,
  directiveFingerprint,
  directiveKeyFor,
  directiveReceipt,
  directiveRequest,
  oneShotDirections,
  persistentDirections,
  randomRequestKey,
  runningDirectionsHint,
  type DirectiveDraft,
} from '@/lib/directive'
import {
  activeInstructions,
  instructionPreview,
  instructionSummary,
  sortInstructions,
} from '@/lib/instructions'
import { statusLabel } from '@/lib/format'
import { usePoll } from '@/lib/hooks'
import type { DirectiveResponse, DirectiveTarget } from '@/lib/types'

type ComposerProps = {
  onSent: (result: DirectiveResponse) => void
  paused: boolean
  runningExecutors: number
  autoFocus?: boolean
  /** Jump to the persistent-instruction surface; absent where there is no such page. */
  onManageInstructions?: () => void
  /** What that jump is called where it lands somewhere other than the panel. */
  manageLabel?: string
  /** Bumped by the management surface so the inline echo refetches after an edit there. */
  refreshInstructions?: number
  /** Chat-page density: choices on one row, no explanatory paragraph, collapsed set. */
  compact?: boolean
}

export function ChatComposer(props: ComposerProps) {
  // No location key here: the composer is mounted per page right column, and the
  // project switch already remounts the pages that host it.
  return <Composer {...props} />
}

function Composer({ onSent, paused, runningExecutors, autoFocus = false, onManageInstructions, manageLabel = '更正/撤销', refreshInstructions = 0, compact = false }: ComposerProps) {
  // 一次性 is the safe default: it reads once and is not inherited. Turning the
  // switch to 持续 changes the target default from 指挥官 to 整个项目.
  const [persistent, setPersistent] = useSessionState('composer.persistent', false)
  const [target, setTarget] = useSessionState<DirectiveTarget>('composer.target', 'commander')
  const [intentID, setIntentID] = useSessionState('composer.intentID', '')
  const [steer, setSteer] = useState(false)
  // Explicit operator mark: this persistent directive is also an authorization
  // change (widen or narrow). It has no meaning on a one-shot hint, which has no
  // validity window, so the option only exists while 持续 is selected.
  const [asAuthorization, setAsAuthorization] = useState(false)
  const [text, setText] = useSessionState('composer.text', '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [receipt, setReceipt] = useState('')
  // Bumped on a successful send so the inline standing set refetches at once.
  const [sentSeq, setSentSeq] = useState(0)

  const intents = usePoll(api.intents, 8000)
  // One-shot may only reach a direction that is not settled; a persistent
  // instruction may scope to any direction (it applies to later descendants too).
  const directions = useMemo(
    () =>
      persistent
        ? persistentDirections(intents.data ?? [])
        : oneShotDirections(intents.data ?? []),
    [persistent, intents.data],
  )
  const selected = target === 'intent' ? directions.find((i) => i.id === intentID) : undefined
  const steerAllowed = !persistent && target === 'intent' && canSteer(selected)
  const steerActive = steer && steerAllowed

  // A selected direction can become settled while the poll refreshes (or while
  // switching 续效); drop the stale selection instead of sending to it.
  useEffect(() => {
    if (!intents.data || intents.error || target !== 'intent' || !intentID) return
    if (!directions.some((i) => i.id === intentID)) setIntentID('')
  }, [target, intentID, directions, intents.data, intents.error])

  // One key per directive: a transport retry of the same content/choices must
  // not create a second row; changing anything regenerates the key.
  const [savedKey, setSavedKey] = useSessionState('composer.request', { fingerprint: '', key: '' })
  const keyRef = useRef(savedKey)
  const draftScope = `draft:${window.location.pathname}`
  const active = useRef(true)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])

  function choosePersistent(next: boolean) {
    if (busy) return
    setPersistent(next)
    // Keep the target valid for the chosen lifetime.
    setTarget((current) => {
      if (next && (current === 'commander' || current === 'executors')) return 'project'
      if (!next && current === 'project') return 'commander'
      return current
    })
    setSteer(false)
    if (!next) setAsAuthorization(false)
    setError('')
    setReceipt('')
  }

  function chooseTarget(next: DirectiveTarget) {
    if (busy) return
    setTarget(next)
    setSteer(false)
    setError('')
    setReceipt('')
  }

  function hint(): string {
    if (target === 'executors') return '一次性发给当前在办的执行者；固定收件名单，每人独立回执，部分失败可用同一请求重试。'
    if (persistent) {
      if (target === 'project') {
        return '持续到撤销：影响之后新建的所有方向；暂停期间先保存，恢复后生效。'
      }
      return `持续到撤销：${selected?.title || '该方向'} 及其后代任务会继承，直到撤销。`
    }
    if (target === 'commander') {
      return paused
        ? '项目已暂停：一次性提示会排队，点「继续项目」后由指挥官的下一轮读取。'
        : `只影响指挥官下一轮决策，不建立持久指令；${runningDirectionsHint(runningExecutors)}。`
    }
    if (steerActive) {
      return '会停掉当前 run，用同一轨迹开新 turn；已写出的证据保留，不建立持久指令。'
    }
    if (selected) {
      return `一次性提示只发给这个方向（${statusLabel(selected.status)}）：正在跑就插话，否则排到它下一轮；不进其他方向，也不进后续提示词。`
    }
    return '一次性提示只发给选中的方向；不建立持久指令。'
  }

  async function send() {
    const content = text.trim()
    if (!content || busy) return
    if (target === 'intent' && !intentID) {
      setError('先选一个方向再下发')
      return
    }
    const draft: DirectiveDraft = {
      text: content,
      persistent,
      target,
      intentID: target === 'intent' ? intentID : '',
      steer: steerActive,
      asAuthorization: persistent && asAuthorization,
    }
    keyRef.current = directiveKeyFor(keyRef.current, directiveFingerprint(draft), randomRequestKey)
    setSavedKey(keyRef.current)
    setBusy(true)
    setError('')
    setReceipt('')
    try {
      const result = await api.directive(directiveRequest(draft, keyRef.current.key))
      if (!active.current) {
        // A successful response may arrive after navigating away. Only clear
        // this submission, never a newer draft written in the other view.
        const stored = readViewMemory<{ key: string }>(draftScope, 'composer.request')
        if (stored?.key === keyRef.current.key &&
            readViewMemory<string>(draftScope, 'composer.text')?.trim() === content &&
            (readViewMemory<string>(draftScope, 'composer.target') ?? 'commander') === target &&
            (readViewMemory<boolean>(draftScope, 'composer.persistent') ?? false) === persistent &&
            (target !== 'intent' || readViewMemory<string>(draftScope, 'composer.intentID') === intentID)) {
          writeViewMemory(draftScope, 'composer.text', '')
        }
        return
      }
      setReceipt(directiveReceipt(result))
      // Only a successful server answer clears the draft: never optimistic.
      setText('')
      setSteer(false)
      setAsAuthorization(false)
      keyRef.current = { fingerprint: '', key: '' }
      setSavedKey(keyRef.current)
      setSentSeq((current) => current + 1)
      onSent(result)
    } catch (e) {
      if (active.current) setError(directiveErrorMessage(e))
    } finally {
      if (active.current) setBusy(false)
    }
  }

  const choiceClass = compact ? 'h-7 px-1.5 text-[10.5px]' : 'h-8 px-2 text-[11px]'
  const sendFromKeyboard = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    // Safari dispatches the confirming Enter before `compositionend`, so
    // `isComposing` alone is not enough: keyCode 229 is the IME marker.
    const native = e.nativeEvent
    if (e.key === 'Enter' && !e.shiftKey && !native.isComposing && native.keyCode !== 229) {
      e.preventDefault()
      void send()
    }
  }
  const placeholder = persistent
    ? target === 'project'
      ? '写持续有效的边界或要求：会影响之后新建的所有方向，直到撤销。'
      : '写这个方向及其后代都适用的边界或要求，直到撤销。'
    : target === 'commander'
      ? '例如：下一步先补 /api 的授权模型；拿到账号或白名单 IP 再继续，别在授权外试探。'
      : steerActive
        ? '写清换向后的下一步：例如停掉字典铺路，收窄到登录口做单变量差分。'
        : '例如：这个方向先停在只读验证；portal.example 主站也在范围内。'

  if (compact) {
    return (
      <div className="shrink-0 space-y-1.5 border-t border-hairline p-2.5" data-directive-composer data-compact="true">
        <div className="flex items-end gap-2">
          <Textarea
            rows={2}
            value={text}
            disabled={busy}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={sendFromKeyboard}
            maxLength={4000}
            autoFocus={autoFocus}
            enterKeyHint="send"
            aria-describedby="composer-hint"
            placeholder={placeholder}
            className="min-h-[44px] flex-1 text-[12px] leading-relaxed placeholder:text-ink-2"
            aria-label="下发内容"
          />
          <Button
            size="sm"
            className="mb-0.5 shrink-0"
            disabled={busy || !text.trim() || (target === 'intent' && !intentID)}
            onClick={() => void send()}
          >
            {busy && <Spinner size="sm" className="text-primary-foreground" />}
            {busy ? '处理中…' : persistent ? '创建指令' : steerActive ? '停并换向' : '发送'}
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <div className="flex items-center gap-1.5">
            <span className="text-[10px] text-ink-3">时效</span>
            <ToggleGroup
              value={persistent ? 'persist' : 'once'}
              onValueChange={(value) => choosePersistent(value === 'persist')}
              ariaLabel="下发时效"
              disabled={busy}
            >
              <ToggleGroupItem value="once" disabled={busy} className={choiceClass}>一次性</ToggleGroupItem>
              <ToggleGroupItem value="persist" disabled={busy} className={choiceClass}>持续</ToggleGroupItem>
            </ToggleGroup>
          </div>
          <div className="flex items-center gap-1.5">
            <span className="text-[10px] text-ink-3">对象</span>
            <ToggleGroup value={target} onValueChange={(value) => chooseTarget(value as DirectiveTarget)} disabled={busy} ariaLabel="下发对象">
              {(persistent
                ? [{value:'project',label:'整个项目'},{value:'intent',label:'某个方向'}]
                : [{value:'commander',label:'指挥官'},{value:'executors',label:'全部执行者'},{value:'intent',label:'某个方向'}]
              ).map(item=><ToggleGroupItem key={item.value} value={item.value} className={choiceClass}>{item.label}</ToggleGroupItem>)}
            </ToggleGroup>
          </div>
          <span className="tw-nums ml-auto font-mono text-[10px] text-ink-3">{text.trim().length}/4000</span>
        </div>

        {target === 'intent' && (
          <div className="space-y-1.5">
            <Select
              value={intentID || undefined}
              onValueChange={(value) => {
                setIntentID(value)
                setSteer(false)
              }}
              disabled={busy || directions.length === 0}
            >
              <SelectTrigger className="h-7 text-[11px]" aria-label="选择下发方向">
                <SelectValue placeholder={directions.length > 0 ? '选择方向' : '没有可选方向'} />
              </SelectTrigger>
              <SelectContent className="max-w-[calc(100vw-2rem)]">
                {directions.map((intent) => (
                  <SelectItem key={intent.id} value={intent.id} className="text-[12px]">
                    <span className="truncate">{intent.title}</span>
                    <span className="ml-1.5 font-mono text-[11px] text-ink-3">{intent.id.slice(0, 12)}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {steerAllowed && (
              <label className="flex items-start gap-2 text-[10.5px] text-ink-2">
                <Checkbox
                  checked={steerActive}
                  onCheckedChange={(checked) => setSteer(Boolean(checked))}
                  disabled={busy}
                  aria-label="立即停并换向"
                  className="mt-0.5"
                />
                <span>立即停并换向（steer）：停掉当前 run，用同一轨迹继续。</span>
              </label>
            )}
          </div>
        )}

        {persistent && (
          <label className="flex items-start gap-2 text-[10.5px] text-ink-2">
            <Checkbox
              checked={asAuthorization}
              onCheckedChange={(checked) => setAsAuthorization(Boolean(checked))}
              disabled={busy}
              aria-label="作为授权变更（放宽/收窄）"
              className="mt-0.5"
            />
            <span>
              作为授权变更（放宽/收窄）
              <span className="block leading-4 text-ink-3">
                勾选后这条原文按来源消息 id 记进账本授权链，之后每轮按 id 重读；不勾选只保存为指令文本。
              </span>
            </span>
          </label>
        )}

        {persistent && (
          <PersistentSet
            onManage={onManageInstructions}
            manageLabel={manageLabel}
            refresh={sentSeq + refreshInstructions}
            compact
          />
        )}

        <p id="composer-hint" className="sr-only">{hint()}</p>
        {(paused || persistent || target === 'intent') && (
          <p className="truncate text-[10.5px] leading-4 text-ink-3" title={hint()}>{hint()}</p>
        )}
        {error && (
          <div role="alert" className="text-[11px] leading-4 text-destructive">
            {error}
          </div>
        )}
        {receipt && (
          <p data-directive-receipt className="text-[11px] leading-5 text-ink-2">
            {receipt}
          </p>
        )}
      </div>
    )
  }

  return (
    <div className="shrink-0 space-y-2 p-3" data-directive-composer>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[10.5px] text-ink-3">时效</span>
          <ToggleGroup
            value={persistent ? 'persist' : 'once'}
            onValueChange={(value) => choosePersistent(value === 'persist')}
            ariaLabel="下发时效"
            disabled={busy}
          >
            <ToggleGroupItem value="once" disabled={busy} className={choiceClass}>
              一次性（默认）
            </ToggleGroupItem>
            <ToggleGroupItem value="persist" disabled={busy} className={choiceClass}>
              持续到撤销
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[10.5px] text-ink-3">对象</span>
          <ToggleGroup value={target} onValueChange={(value) => chooseTarget(value as DirectiveTarget)} disabled={busy} ariaLabel="下发对象">
            {(persistent
              ? [{value:'project',label:'整个项目'},{value:'intent',label:'某个方向'}]
              : [{value:'commander',label:'指挥官'},{value:'executors',label:'全部执行者'},{value:'intent',label:'某个方向'}]
            ).map(item=><ToggleGroupItem key={item.value} value={item.value} className={choiceClass}>{item.label}</ToggleGroupItem>)}
          </ToggleGroup>
        </div>
        <span className="text-[11px] text-ink-3">一次性提示 / 持续指令 / 换向</span>
      </div>

      {target === 'intent' && (
        <div className="space-y-1.5">
          <Select
            value={intentID || undefined}
            onValueChange={(value) => {
              setIntentID(value)
              setSteer(false)
            }}
            disabled={busy || directions.length === 0}
          >
            <SelectTrigger className="h-8 text-[12px]" aria-label="选择下发方向">
              <SelectValue placeholder={directions.length > 0 ? '选择方向' : '没有可选方向'} />
            </SelectTrigger>
            <SelectContent className="max-w-[calc(100vw-2rem)]">
              {directions.map((intent) => (
                <SelectItem key={intent.id} value={intent.id} className="text-[12px]">
                  <span className="truncate">{intent.title}</span>
                  <span className="ml-1.5 font-mono text-[11px] text-ink-3">
                    {intent.id.slice(0, 12)}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {selected && (
            <div className="flex flex-wrap items-center gap-x-2 text-[11px] text-ink-3">
              <span className="font-mono">{selected.id}</span>
              <span>{statusLabel(selected.status)}</span>
            </div>
          )}
          {steerAllowed && (
            <label className="flex items-start gap-2 text-[11px] text-ink-2">
              <Checkbox
                checked={steerActive}
                onCheckedChange={(checked) => setSteer(Boolean(checked))}
                disabled={busy}
                aria-label="立即停并换向"
                className="mt-0.5"
              />
              <span>
                立即停并换向（steer）
                <span className="block text-[11px] leading-4 text-ink-3">
                  停掉当前 run，用同一轨迹继续；不勾选则作为一次性提示排到下一轮。
                </span>
              </span>
            </label>
          )}
        </div>
      )}

      {persistent && (
        <label className="flex items-start gap-2 text-[11px] text-ink-2">
          <Checkbox
            checked={asAuthorization}
            onCheckedChange={(checked) => setAsAuthorization(Boolean(checked))}
            disabled={busy}
            aria-label="作为授权变更（放宽/收窄）"
            className="mt-0.5"
          />
          <span>
            作为授权变更（放宽/收窄）
            <span className="block text-[11px] leading-4 text-ink-3">
              勾选后这条原文按来源消息 id 记进账本授权链，之后每轮按 id 重读；不勾选只保存为指令文本。
            </span>
          </span>
        </label>
      )}

      {persistent && (
        <PersistentSet
          onManage={onManageInstructions}
          manageLabel={manageLabel}
          refresh={sentSeq + refreshInstructions}
          compact={compact}
        />
      )}

      <Textarea
        rows={2}
        value={text}
        disabled={busy}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={sendFromKeyboard}
        maxLength={4000}
        autoFocus={autoFocus}
        enterKeyHint="send"
        aria-describedby="composer-hint"
        placeholder={placeholder}
        className="min-h-[56px] text-[12px] leading-relaxed placeholder:text-ink-2"
        aria-label="下发内容"
      />
      <div className="flex items-center gap-2">
        <span className="tw-nums font-mono text-[11px] text-ink-3">
          {text.trim().length}/4000 · Enter 发送，Shift+Enter 换行
        </span>
        <Button
          size="sm"
          className="ml-auto shrink-0"
          disabled={busy || !text.trim() || (target === 'intent' && !intentID)}
          onClick={() => void send()}
        >
          {busy && <Spinner size="sm" className="text-primary-foreground" />}
          {busy ? '处理中…' : persistent ? '创建指令' : steerActive ? '停并换向' : '发送提示'}
        </Button>
      </div>
      <div className="text-[11px] leading-4 text-ink-3" id="composer-hint">{hint()}</div>
      {error && (
        <div role="alert" className="text-[11px] leading-4 text-destructive">
          {error}
        </div>
      )}
      {receipt && (
        <p data-directive-receipt className="text-[12px] leading-5 text-ink-2">
          {receipt}
        </p>
      )}
    </div>
  )
}

/**
 * The standing constraint set, shown while the operator is about to write one
 * more. It is a read-only echo of the same rows the 持久指令 does the managing:
 * the point is that an existing sentence is visible at the moment of appending,
 * so correction (same row, v+1) is the obvious move and a second contradictory
 * sentence is a visible choice rather than an accident.
 */
function PersistentSet({ onManage, manageLabel, refresh, compact = false }: { onManage?: () => void; manageLabel: string; refresh: number; compact?: boolean }) {
  const set = usePoll(() => api.instructions('', 'active'), 8000)
  // The query already asks for active rows; filtering again keeps the echo from
  // ever presenting a revoked sentence as a standing constraint.
  const rows = useMemo(
    () => sortInstructions(activeInstructions(set.data?.instructions ?? [])),
    [set.data],
  )
  const shown = rows.slice(0, 3)
  // A just-created instruction must appear in the echo immediately: the poll
  // interval is for someone else's edits, not for the operator's own write.
  const reload = set.reload
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return }
    reload()
  }, [refresh, reload])
  return (
    <div data-instruction-set className="rounded-lg border border-hairline bg-surface-2/60 px-2.5 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] font-medium text-ink-2">{instructionSummary(rows)}</span>
        {onManage && (
          <button
            type="button"
            onClick={onManage}
            className="ml-auto text-[11px] text-primary transition-colors hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          >
            {compact ? `查看${rows.length > 0 ? ` ${rows.length} 条` : ''}` : manageLabel}
          </button>
        )}
      </div>
      {!compact && shown.length > 0 && (
        <ul className="mt-1 space-y-1">
          {shown.map((row) => (
            <li key={row.id} className="flex min-w-0 gap-1.5 text-[11px] leading-4 text-ink-3">
              <span className="tw-nums shrink-0 font-mono">v{row.version}</span>
              <span className="shrink-0">{row.intent_id ? '方向' : '项目'}</span>
              <span className="truncate">{instructionPreview(row.text, 60)}</span>
            </li>
          ))}
          {rows.length > shown.length && (
            <li className="text-[11px] leading-4 text-ink-3">…另有 {rows.length - shown.length} 条</li>
          )}
        </ul>
      )}
      {!compact && (
        <p className="mt-1 text-[11px] leading-4 text-ink-3">
          同一主题的改口请更正原条目（变新版本）；两条相反的指令会同时进入后续提示词。
        </p>
      )}
    </div>
  )
}
