// The graph node panel's state vocabulary: readiness is only asked while a
// direction can still run; the header and child rows speak the operator's words
// instead of graph enums; the latest run line and the collapsible warnings keep
// the panel from burying its own answer. These are source contracts because the
// panel is a React tree loaded lazily with API hooks; the pure rules behind the
// readiness gate are unit-tested in intent-detail.test.ts.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const panel = await readFile(
  fileURLToPath(new URL('../src/components/graph/GraphNodePanel.tsx', import.meta.url)),
  'utf8',
)

test('readiness is not polled for a direction that cannot run', () => {
  assert.match(panel, /readinessApplies\(intent\?\.status, intent\?\.hold\)/)
  assert.match(panel, /showReadiness \? 10000 : 0/)
  assert.match(panel, /readinessNotice\(intent\.status, intent\.hold\)/)
})

test('the header and child rows use operator words, not graph enums', () => {
  assert.match(panel, /nodeTypeLabel\(node\.type, fact\?\.kind\)/)
  assert.match(panel, /runStatusLabel\(\{ status: child\.last_run_status, cancel_cause: child\.cancel_cause \}\)/)
  assert.doesNotMatch(panel, /\{child\.stop_reason\}/, 'the raw run enum is not printed')
})

test('the latest run line and the collapsible warnings are wired', () => {
  assert.match(panel, /<RunSummaryLine run=\{latestRun\} onOpenRun=\{onOpenRun\} \/>/)
  assert.match(panel, /<details className="group rounded-lg border border-warning\/25/)
  assert.match(panel, /group-open:hidden/)
})
