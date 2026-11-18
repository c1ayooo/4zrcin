import { useState } from 'react'
import { ChevronDown, ListChecks } from 'lucide-react'
import { GOAL_PRESETS, type GoalPreset } from '@/lib/goal-presets'

/**
 * An optional list of common goal shapes. Picking one fills 起点/终点 drafts; it
 * never submits, and the operator can keep editing the text afterwards. The list
 * is collapsed by default so the form stays a two-field form for people who
 * already know their goal.
 */
export function GoalPresets({
  onPick,
  disabled = false,
}: {
  onPick: (preset: GoalPreset) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-lg border border-hairline bg-surface-2/40">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center gap-1.5 px-2.5 py-2 text-left text-[12px] font-medium text-ink-2 transition-colors hover:text-ink"
      >
        <ListChecks className="h-3.5 w-3.5 shrink-0 text-ink-3" aria-hidden="true" />
        常用目标模板
        <span className="ml-auto flex items-center gap-1 text-[11px] font-normal text-ink-3">
          {open ? '收起' : `${GOAL_PRESETS.length} 个可选`}
          <ChevronDown
            className={`h-3.5 w-3.5 shrink-0 transition-transform duration-150 ${open ? '' : '-rotate-90'}`}
            aria-hidden="true"
          />
        </span>
      </button>
      {open && (
        <div
          role="list"
          aria-label="常用目标模板"
          className="tw-scroll max-h-56 space-y-1 overflow-y-auto border-t border-hairline p-2"
        >
          {GOAL_PRESETS.map((preset) => (
            <button
              key={preset.id}
              type="button"
              role="listitem"
              data-goal-preset={preset.id}
              disabled={disabled}
              onClick={() => onPick(preset)}
              title={`${preset.statement}\n\n达成时预期证据：${preset.evidence}`}
              className="block w-full rounded-md px-2 py-1.5 text-left transition-colors hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-40"
            >
              <span className="block text-[12px] font-medium text-ink">{preset.label}</span>
              <span className="mt-0.5 line-clamp-2 block text-[11px] leading-4 text-ink-3">{preset.statement}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
