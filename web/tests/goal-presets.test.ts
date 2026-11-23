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
import { PROJECT_SCENARIOS, scenarioPreset } from '../src/lib/project-scenarios.ts'

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
    // Exactly the three goal texts plus the picker's own two labels. A preset
    // never carries a fourth text field: the 内置约束 document belongs to the
    // operator alone, so a preset that grew a `constraints` field would be
    // writing it on their behalf.
    assert.deepEqual(Object.keys(preset).sort(), ['evidence', 'id', 'label', 'start', 'statement'],
      `${preset.id}: a preset fills 起点/终点/证据 and nothing else`)
  }
})

test('the broad-coverage preset stops one step before a shell', () => {
  const preset = GOAL_PRESETS.find((p) => p.id === 'broad-coverage-hold')
  assert.ok(preset, 'the wide-coverage engagement shape is built in')
  // 终点 must be judgeable and must state the stop line, otherwise the shape
  // silently turns into "get a shell" for a project that asked for the opposite.
  assert.match(preset.statement, /关联资产/)
  assert.match(preset.statement, /不建立交互式 Shell/)
  assert.match(preset.start, /广覆盖/)
  // The shape must not turn into a per-asset quota: the executor is a model,
  // and how deep to go on each asset is its judgement, not the preset's.
  assert.match(preset.statement, /深浅由执行者判断/)
  assert.doesNotMatch([preset.statement, preset.start].join('\n'), /每台.*(一条|两条)|各留一条|最多.*次/)
  // The expansion is bounded: one hop of related assets is in scope, anything
  // further is only recorded as a candidate — the preset must not read as
  // "follow every link and scan it".
  assert.match(preset.start, /一层关联资产/)
  assert.match(preset.start, /只登记候选/)
  // The foothold shape is the one that does open a session; its label says so,
  // so the two shapes cannot be confused in the picker.
  const foothold = GOAL_PRESETS.find((p) => p.id === 'foothold')
  assert.ok(foothold)
  assert.match(foothold.label, /会话/)
})

test('every project scenario fills from a real preset and carries no constraint text', () => {
  const ids = new Set(PROJECT_SCENARIOS.map((scenario) => scenario.id))
  assert.equal(ids.size, PROJECT_SCENARIOS.length, 'scenario ids are unique')
  assert.deepEqual(PROJECT_SCENARIOS.map((scenario) => scenario.label), ['红队', '日常渗透'])
  for (const scenario of PROJECT_SCENARIOS) {
    const preset = scenarioPreset(scenario)
    assert.ok(preset, `${scenario.id} points at a preset the library does not have`)
    // The text a scenario writes into the form never states a boundary in the
    // operator's place: 内置约束 belongs to whoever runs the engagement.
    const written = [preset.label, preset.statement, preset.start, preset.evidence].join('\n')
    assert.doesNotMatch(written, /内置约束|约束文档|硬线/, `${scenario.id} must not carry constraint text`)
    assert.ok(scenario.hint.trim().length > 0, `${scenario.id}: hint`)
  }
  // A scenario carries the two texts it fills and nothing else: no switch over
  // 内置约束, because that document belongs to the operator.
  for (const scenario of PROJECT_SCENARIOS) {
    assert.deepEqual(
      Object.keys(scenario).sort(),
      ['hint', 'id', 'label', 'presetID'],
      `${scenario.id} must not carry a constraint switch`,
    )
  }
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
  // The list says out loud that these are sample texts, not rules in force: the
  // stop-line preset only becomes the operator's own words once picked.
  assert.match(JSON.stringify(renderer!.toJSON()), /仅为示例文本，选中后才写入目标/)
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
