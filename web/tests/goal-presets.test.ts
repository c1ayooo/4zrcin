// Optional goal templates.
//
// The console may offer a starting point for a goal, but never a procedure: a
// preset is a falsifiable outcome (终点) plus a template for what is known now
// (起点). These cases pin three things the product boundary depends on:
//
//   1. the library is generic — no target, host, IP, URL, credential or tool;
//   2. picking a preset only fills the drafts (it never submits, and the
//      operator keeps editing the text);
//   3. the goal can be edited from inside the project, not only from 概览: the
//      graph page's 终点 card opens the same editor and refreshes afterwards.
import { after, afterEach, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { act, create, type ReactTestRenderer } from 'react-test-renderer'
import { createTestServer } from './helpers/vite.ts'
import { GOAL_PRESETS, type GoalPreset } from '../src/lib/goal-presets.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let GoalPresets: React.ComponentType<{ onPick: (preset: GoalPreset) => void; disabled?: boolean }>
let renderer: ReactTestRenderer | undefined

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  GoalPresets = (await server.ssrLoadModule('/src/components/GoalPresets.tsx')).GoalPresets
})

afterEach(async () => {
  await act(async () => { renderer?.unmount(); renderer = undefined })
})

after(async () => { await server?.close() })

test('the library offers several distinct, complete goal shapes', () => {
  assert.ok(GOAL_PRESETS.length >= 6, `expected a usable library, got ${GOAL_PRESETS.length}`)
  const ids = new Set<string>()
  for (const preset of GOAL_PRESETS) {
    assert.ok(preset.id.length > 0)
    assert.equal(ids.has(preset.id), false, `duplicate preset id ${preset.id}`)
    ids.add(preset.id)
    assert.ok(preset.label.trim().length > 0, `${preset.id}: label`)
    assert.ok(preset.statement.trim().length >= 12, `${preset.id}: 终点 must be a judgeable sentence`)
    assert.ok(preset.statement.trim().length <= 120, `${preset.id}: 终点 stays one sentence`)
    assert.ok(preset.start.trim().length > 0, `${preset.id}: 起点 template`)
    assert.ok(preset.evidence.trim().length > 0, `${preset.id}: expected evidence shape`)
  }
})

test('the broad-coverage preset stops one step before a shell', () => {
  const preset = GOAL_PRESETS.find((p) => p.id === 'broad-coverage-hold')
  assert.ok(preset, 'the wide-coverage engagement shape is built in')
  // 终点 must be judgeable and must state the stop line, otherwise the shape
  // silently turns into "get a shell" for a project that asked for the opposite.
  assert.match(preset.statement, /每一台|每台/)
  assert.match(preset.statement, /不建立交互式 Shell/)
  assert.match(preset.start, /广覆盖/)
  // The foothold shape is the one that does open a session; its label says so,
  // so the two shapes cannot be confused in the picker.
  const foothold = GOAL_PRESETS.find((p) => p.id === 'foothold')
  assert.ok(foothold)
  assert.match(foothold.label, /会话/)
})

test('presets carry no target, host, URL, credential or tool name', () => {
  const banned = [
    /\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/, // IPv4 literal
    /https?:\/\//i, // URL
    /\b[\w-]+\.(com|cn|net|org|io|gov)\b/i, // real domain
    /\b(nmap|masscan|sqlmap|metasploit|nuclei|gogo|fscan|burp|cobalt)\b/i, // tooling
    /admin:\S+/, // credential pair
  ]
  for (const preset of GOAL_PRESETS) {
    const text = [preset.label, preset.statement, preset.start, preset.evidence].join('\n')
    for (const pattern of banned) {
      assert.equal(pattern.test(text), false, `${preset.id} must stay generic (matched ${pattern})`)
    }
  }
})

test('the picker is collapsed, and picking fills the drafts instead of submitting', async () => {
  const picked: GoalPreset[] = []
  await act(async () => {
    renderer = create(React.createElement(GoalPresets, { onPick: (preset) => picked.push(preset) }))
  })
  const header = () => renderer!.root.findAllByProps({ 'aria-expanded': true })
    .concat(renderer!.root.findAllByProps({ 'aria-expanded': false }))[0]
  assert.equal(header().props['aria-expanded'], false, 'collapsed by default')
  assert.deepEqual(picked, [], 'mounting the picker submits nothing')

  await act(async () => { header().props.onClick() })
  assert.equal(header().props['aria-expanded'], true)
  const items = renderer!.root.findAllByProps({ role: 'listitem' })
  assert.equal(items.length, GOAL_PRESETS.length, 'every preset is offered once')
  assert.deepEqual(picked, [], 'opening the list submits nothing')

  const target = GOAL_PRESETS[2]
  await act(async () => { items[2].props.onClick() })
  assert.deepEqual(picked.map((p) => p.id), [target.id], 'the picked preset reaches the caller')
})

test('both goal dialogs offer the presets, and the graph page can edit a goal', () => {
  const read = (path: string) => readFileSync(fileURLToPath(new URL(`../src/${path}`, import.meta.url)), 'utf8')
  for (const dialog of ['components/CreateGoalDialog.tsx', 'components/GoalEditorDialog.tsx']) {
    const source = read(dialog)
    assert.match(source, /GoalPresets/, `${dialog} offers the optional templates`)
    assert.doesNotMatch(source, /GOAL_PRESETS\s*\[/, `${dialog} never auto-picks a preset`)
  }
  // The project's own graph page edits an unmet goal in place (终点 card), and
  // re-reads the snapshot after the change so the card and map agree.
  const panel = read('components/graph/GraphPanel.tsx')
  assert.match(panel, /import \{ GoalEditorDialog/, 'the 终点 card opens the shared goal editor')
  assert.match(panel, /onClick=\{\(\) => setEditingGoal\(g\)\}/, 'every unmet goal gets a 管理 action')
  assert.match(panel, /管理/, 'the action is labelled 管理 like the overview card')
  assert.match(panel, /onSaved=\{\(\) => \{[\s\S]*?refresh\(\)/, 'saving refreshes the snapshot and board')
})
