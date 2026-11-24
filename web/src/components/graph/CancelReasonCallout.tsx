import { Callout } from '@cyber/ui'
import { cancelCauseText, holdActorLabel, runCancelReason } from '@/lib/format'
import type { Run } from '@/lib/types'

/**
 * Why a held direction stopped, in the same words the run page uses. A held
 * intent settles to failed+hold, so the status chip alone cannot say "stopped
 * by the commander to reclaim the slot"; this carries the run's recorded
 * reason. A stop before the first dispatch has no cancelled run, so the
 * intent's own HoldReason is the fallback (the ledger's reason, not a rewrite).
 *
 * The run id / time / status live on the RunSummaryLine next to it, so they are
 * not repeated here. `heldBy` is the intent's HeldBy (who set the stop), not
 * the run's cancel cause: operator and commander stops share the cause text,
 * and only the holder answers "may this be restarted?".
 */
export function CancelReasonCallout({
  run,
  heldBy,
  holdReason,
}: {
  run?: Run
  heldBy?: string
  holdReason?: string
}) {
  const who = holdActorLabel(heldBy)
  const body = run ? runCancelReason(run) : holdReason?.trim() || '已停止（原因未记录）'
  const title = run ? cancelCauseText(run.cancel_cause) : holdReason?.trim() || ''
  return (
    <Callout tone="warning" className="leading-relaxed">
      <div className="flex items-start gap-2">
        <span className="shrink-0 font-semibold text-warning">取消原因{who ? ` · ${who}` : ''}</span>
        <span className="tw-break min-w-0 flex-1 text-ink-2" title={title}>
          {body}
        </span>
      </div>
    </Callout>
  )
}
