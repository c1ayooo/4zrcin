import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

// The token split is folded into the run detail, but its numbers are the
// project's. These are source-level assertions on purpose: the run detail is a
// large polling view, and what must not regress is (a) the card stays reachable
// there, (b) the fetch only happens once the operator opens it, and (c) the
// summary keeps saying the numbers are the project's rather than this run's.
const runs = await readFile(fileURLToPath(new URL('../src/views/Runs.tsx', import.meta.url)), 'utf8')

test('the run detail mounts the token split', () => {
  assert.match(runs, /<RunTokenSection key=\{`tokens-\$\{current\.id\}`\} runID=\{current\.id\} \/>/)
  assert.match(runs, /<TokenBreakdownCard breakdown=\{usage\.data\} error=\{usage\.error\} loading=\{usage\.loading\} \/>/)
})

test('the split is read only after the disclosure opens', () => {
  // Selecting a settled run must not aggregate every run in the project.
  assert.match(runs, /open \? api\.usageBreakdown\(\) : Promise\.resolve\(undefined\)/, 'the read has to be gated on the disclosure being open')
  assert.match(runs, /Promise\.resolve\(undefined\)\), \[open\]\)/, 'the gate has to re-run when the disclosure toggles')
})

test('the summary states the scope, so the numbers are not read as this run\'s', () => {
  assert.match(runs, /Token 分账 · 本项目累计（指挥官 \/ 执行者 \/ 裁判）/)
})
