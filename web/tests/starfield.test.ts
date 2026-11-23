// 星图观感是纯绘制层：这里钉住「谁发光」的规则和没有 DOM 时的降级，不让它悄悄
// 变成另一种信息层级——光晕只是亮度，不改节点、不改颜色、不改命中。
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let mod: {
  MAX_GLOW: number
  glowIds: (
    visible: ReadonlyArray<{ id: string; type: string }>,
    picks: { selected: string | null; hovered: string | null; matches: ReadonlySet<string> | null; keyboard: string | null },
    cap?: number,
  ) => Set<string>
  starSprite: (color: string) => unknown
  paintStarfield: (ctx: unknown, w: number, h: number, cam: { x: number; y: number; k: number }, motion: boolean) => boolean
}

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  mod = (await server.ssrLoadModule('/src/graph/starfield.ts')) as typeof mod
})

after(async () => {
  await server.close()
})

const nodes = [
  ...Array.from({ length: 300 }, (_, i) => ({ id: `f:${i}`, type: 'fact' })),
  ...Array.from({ length: 40 }, (_, i) => ({ id: `s:${i}`, type: 'step' })),
  { id: 'p:1', type: 'project' },
]

test('picked nodes always glow, then directions, then facts, and the frame keeps a cap', () => {
  const picked = mod.glowIds(nodes, {
    selected: 'f:299',
    hovered: 'f:298',
    matches: new Set(['f:297']),
    keyboard: 'p:1',
  })
  for (const id of ['f:299', 'f:298', 'f:297', 'p:1']) {
    assert.ok(picked.has(id), `${id} is picked, so it glows`)
  }
  const steps = nodes.filter((n) => n.type === 'step')
  assert.ok(steps.every((n) => picked.has(n.id)), 'every direction gets a glow before background facts')
  assert.ok(picked.size <= mod.MAX_GLOW, `the frame stays under the glow cap (${picked.size} <= ${mod.MAX_GLOW})`)
  assert.ok(picked.has('f:0'), 'remaining slots go to facts')

  const narrow = mod.glowIds(nodes, { selected: null, hovered: null, matches: null, keyboard: null }, 10)
  assert.equal(narrow.size, 10, 'a lower cap is honoured for background picks')
})