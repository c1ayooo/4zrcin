import { SEVERITY_TIERS, severityBucket, severityLabel } from './format.ts'
import type { ReportFinding } from './types.ts'

export interface SeverityTally {
  tier: string
  label: string
  count: number
}

/**
 * Count the findings a report already carries by their declared grade.
 *
 * This is a tally of values that are already in `findings.json` — the console
 * does not grade, re-grade, or infer a missing level. Order follows the agreed
 * tier order (critical first) so the same report reads the same everywhere;
 * ungraded findings are a bucket of their own and never folded into `low`.
 */
export function tallyFindingSeverities(findings: ReportFinding[]): SeverityTally[] {
  const counts = new Map<string, number>()
  let unlabelled = 0
  for (const finding of findings) {
    const tier = severityBucket(finding.severity)
    if (!tier) {
      unlabelled++
      continue
    }
    counts.set(tier, (counts.get(tier) ?? 0) + 1)
  }

  const ordered: SeverityTally[] = SEVERITY_TIERS.map((tier) => ({
    tier,
    label: severityLabel(tier),
    count: counts.get(tier) ?? 0,
  }))
  // A value outside the known tiers (only possible from hand-edited data) is
  // shown as-is rather than dropped: hiding it would under-report the report.
  for (const [tier, count] of counts) {
    if (!(SEVERITY_TIERS as readonly string[]).includes(tier)) {
      ordered.push({ tier, label: severityLabel(tier), count })
    }
  }
  if (unlabelled > 0) ordered.push({ tier: 'unlabelled', label: '未标注', count: unlabelled })
  return ordered
}
