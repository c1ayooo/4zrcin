import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

// FA2 (task-14) contract tests: the "project views and data-dense pages"
// workstream is mostly CSS/markup, so the guard rails are structural. These are
// deliberately source-level assertions: the web test runner does not render CSS,
// so a semantic table can silently lose its caption/sticky header and still
// "pass" a DOM-only suite.
async function source(kind: 'views' | 'components', name: string): Promise<string> {
  return readFile(fileURLToPath(new URL(`../src/${kind}/${name}`, import.meta.url)), 'utf8')
}

const TABLES = ['Fleet.tsx', 'Overview.tsx'] as const
const DEFERRED_LISTS = [
  ['views', 'Workers.tsx'],
  ['views', 'Traffic.tsx'],
  ['views', 'Runs.tsx'],
  ['views', 'Messages.tsx'],
  ['views', 'Vulns.tsx'],
  ['views', 'Evidence.tsx'],
  ['views', 'Projects.tsx'],
  ['views', 'Decisions.tsx'],
  ['views', 'Nodes.tsx'],
  ['views', 'Assets.tsx'],
  ['views', 'POCs.tsx'],
  ['views', 'Knowledge.tsx'],
  ['views', 'Playbooks.tsx'],
  ['views', 'ExternalActions.tsx'],
  ['views', 'Reports.tsx'],
  ['components', 'FocusPanel.tsx'],
] as const

test('data tables carry caption, column/row scopes and a named scroll region', async () => {
  const fleet = await source('views', 'Fleet.tsx')
  // Guide `responsive-table`: sticky headers only work inside an explicit scroll
  // container, and that container has to be focusable + named for keyboard and
  // screen-reader users.
  assert.match(fleet, /<caption id="fleet-tasks-caption"/, 'Fleet table needs a caption')
  assert.match(fleet, /role="region"/, 'Fleet table needs a named scroll region')
  assert.match(fleet, /aria-labelledby="fleet-tasks-caption"/, 'region must be named by the caption')
  assert.match(fleet, /tabIndex=\{0\}/, 'scroll region must be keyboard focusable')
  assert.match(fleet, /sticky top-0/, 'thead must be sticky inside the scroll container')
  assert.match(fleet, /border-separate/, 'sticky + cell borders need border-collapse: separate')
  assert.match(fleet, /<th scope="col"/, 'column headers need scope=col')
  assert.match(fleet, /<th scope="row"/, 'the identifying cell needs scope=row')
  // Responsive columns must react to the table's own container, not the viewport.
  assert.match(fleet, /@container/, 'table wrapper must be a container-query context')
  assert.match(fleet, /@sm:table-cell/, 'responsive columns must use container variants')
  assert.doesNotMatch(fleet, /(^|[^@\w])sm:table-cell/, 'viewport breakpoints must not drive table columns')

  const overview = await source('views', 'Overview.tsx')
  assert.match(overview, /<caption/, 'Overview table needs a caption')
  assert.match(overview, /scope="col"/, 'Overview headers need scope=col')
  assert.match(overview, /scope="row"/, 'Overview rows need a row header')
})

test('deferred rendering always pairs content-visibility with contain-intrinsic-size', async () => {
  let deferred = 0
  for (const [kind, name] of DEFERRED_LISTS) {
    const src = await source(kind, name)
    assert.ok(src.length > 0, `${name} must exist`)
    // Hard rule from `defer-rendering-heavy-content`: unpaired content-visibility
    // collapses off-screen rows to 0px and makes the scrollbar jump.
    if (src.includes('content-visibility')) deferred++
    const cvis = /\[content-visibility:auto\]/.exec(src)
    if (cvis) {
      assert.match(src, /\[contain-intrinsic-size:auto_[^\]\s]+\]/, `${name}: missing contain-intrinsic-size pair`)
    }
  }
  assert.ok(deferred >= 10, `expected >=10 views to defer off-screen rows, got ${deferred}`)
})

test('first rows are never deferred (below-the-fold-only rule)', async () => {
  const workers = await source('views', 'Workers.tsx')
  assert.match(workers, /defer=\{index >= 6\}/, 'WorkerRow must only defer rows below the first viewport')
  const traffic = await source('views', 'Traffic.tsx')
  assert.match(traffic, /index < 8 \? undefined : '\[contain-intrinsic-size/, 'Traffic must keep its first rows uncontained')
  const runs = await source('views', 'Runs.tsx')
  assert.match(runs, /index >= 6 \? '\[contain-intrinsic-size/)
})

test('animations are disabled under prefers-reduced-motion', async () => {
  const animated: [('views' | 'components'), string][] = [
    ['views', 'Access.tsx'],
    ['views', 'Assets.tsx'],
    ['views', 'Evidence.tsx'],
    ['views', 'ExternalActions.tsx'],
    ['views', 'Memory.tsx'],
    ['views', 'Messages.tsx'],
    ['views', 'Nodes.tsx'],
    ['views', 'Overview.tsx'],
    ['views', 'Playbooks.tsx'],
    ['views', 'ProjectState.tsx'],
    ['views', 'Runs.tsx'],
    ['views', 'Toolbox.tsx'],
    ['views', 'Traffic.tsx'],
    ['views', 'Workers.tsx'],
    ['components', 'FocusPanel.tsx'],
  ]
  for (const [kind, name] of animated) {
    const src = await source(kind, name)
    for (const match of src.matchAll(/animate-(spin|pulse|ping)/g)) {
      // A guarded animation either opts in with motion-safe or opts out with
      // motion-reduce on the same element/class string.
      const tail = src.slice(Math.max(0, match.index - 400), match.index + 260)
      assert.ok(
        tail.includes('motion-reduce:animate-none') || tail.includes('motion-safe:animate'),
        `${name}: unguarded animate-${match[1]} (wrap in motion-reduce/motion-safe)`,
      )
    }
  }
})

test('filter fields opt into the right keyboard/autofill semantics', async () => {
  const fields: [('views' | 'components'), string][] = [
    ['views', 'Workers.tsx'],
    ['views', 'Traffic.tsx'],
    ['views', 'Runs.tsx'],
    ['views', 'Vulns.tsx'],
    ['views', 'POCs.tsx'],
    ['views', 'Toolbox.tsx'],
    ['views', 'ExternalActions.tsx'],
  ]
  for (const [kind, name] of fields) {
    const src = await source(kind, name)
    assert.match(src, /type="search"/, `${name}: filter input should be type=search`)
    assert.match(src, /inputMode="search"/, `${name}: filter input needs inputMode`)
    assert.match(src, /enterKeyHint="search"/, `${name}: filter input needs enterKeyHint`)
    assert.match(src, /autoComplete="off"/, `${name}: filter input must not autofill`)
  }
})

test('data-dense lists use list semantics and live status regions', async () => {
  const lists: [('views' | 'components'), string][] = [
    ['views', 'Workers.tsx'],
    ['views', 'Traffic.tsx'],
    ['views', 'Runs.tsx'],
    ['views', 'Evidence.tsx'],
    ['views', 'Vulns.tsx'],
    ['views', 'Decisions.tsx'],
    ['views', 'Projects.tsx'],
    ['views', 'Nodes.tsx'],
    ['views', 'POCs.tsx'],
    ['views', 'Playbooks.tsx'],
    ['components', 'FocusPanel.tsx'],
  ]
  for (const [kind, name] of lists) {
    const src = await source(kind, name)
    // Tailwind preflight removes `list-style`, which makes Safari drop list
    // semantics; these lists must restore them explicitly.
    assert.match(src, /<ul role="list"[^>]*aria-label=/, `${name}: list needs role=list + an accessible name`)
    assert.match(src, /role="status"/, `${name}: needs a polite status region`)
  }
})

test('scroll containers contain overscroll and style their scrollbar', async () => {
  const panes: [('views' | 'components'), string][] = [
    ['views', 'Traffic.tsx'],
    ['views', 'Runs.tsx'],
    ['views', 'Messages.tsx'],
    ['views', 'Nodes.tsx'],
    ['views', 'Playbooks.tsx'],
    ['views', 'Knowledge.tsx'],
    ['views', 'Projects.tsx'],
    ['views', 'Overview.tsx'],
    ['components', 'FocusPanel.tsx'],
    ['components', 'EvidencePreview.tsx'],
  ]
  for (const [kind, name] of panes) {
    const src = await source(kind, name)
    assert.match(src, /overscroll-contain/, `${name}: scroll container must contain overscroll`)
  }
  for (const [kind, name] of [
    ['views', 'Traffic.tsx'],
    ['views', 'Runs.tsx'],
    ['views', 'Knowledge.tsx'],
    ['components', 'FocusPanel.tsx'],
  ] as const) {
    const src = await source(kind, name)
    assert.match(src, /scrollbar-color:var\(--ink-3\)_transparent/, `${name}: scrollbar should use the token pair`)
  }
})
