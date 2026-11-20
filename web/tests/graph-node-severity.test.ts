import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

// Clicking a vulnerability node must answer "how severe" without leaving the
// graph. The grade lives on the fact; this pins that the click panel renders it
// (it is a different component from the hover inspector) and that the model
// cache rebuilds when only the grade changes.
const panel = await readFile(
  fileURLToPath(new URL('../src/components/graph/GraphNodePanel.tsx', import.meta.url)),
  'utf8',
)
const cache = await readFile(fileURLToPath(new URL('../src/lib/graph-model-cache.ts', import.meta.url)), 'utf8')

test('the graph node panel renders the fact grade', () => {
  assert.match(panel, /<Prop[\s\S]{0,200}label="等级"/)
  assert.match(panel, /severityVariant\(fact\.severity\)/)
  assert.match(panel, /severityLabel\(fact\.severity\)/)
  // Unlabelled is said, not shown as low.
  assert.match(panel, /未标注/)
})

test('the model cache treats the grade as a rebuild trigger', () => {
  assert.match(cache, /f\.severity \?\? ''/)
})
