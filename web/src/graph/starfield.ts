// 星图观感里「节点像星」的那一层：预渲染的径向光晕精灵 + 一帧该给谁发光。
//
// 背景不在这里——用户口径是背景回到点阵底，星图感只留在力导向的节点、光晕与航道。
// 精灵是离屏 canvas（逐帧只 drawImage，不做 gradient 计算），按颜色缓存。
//
// 性能纪律：光晕只给「值得发光」的节点（选中/悬停/命中、方向与目标，以及少量
// 事实），一帧有上限；其余节点仍是亮点。563 节点下这条上限是每帧成本的关键。

/** 一帧最多画多少个光晕；超出的节点只画核心亮点。 */
export const MAX_GLOW = 160
/** 精灵的 CSS 边长：核心 + 外圈光晕。 */
const SPRITE_PX = 44

const spriteCache = new Map<string, HTMLCanvasElement | null>()

function offscreen(w: number, h: number): { canvas: HTMLCanvasElement; ctx: CanvasRenderingContext2D } | null {
  if (typeof document === 'undefined') return null
  try {
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    return ctx ? { canvas, ctx } : null
  } catch {
    return null
  }
}

/**
 * 节点的星形精灵：中心亮核 + 径向光晕，按节点颜色缓存。返回 null 表示没有
 * DOM（单测环境），调用方退回原来的实心点。
 */
export function starSprite(color: string): HTMLCanvasElement | null {
  if (spriteCache.has(color)) return spriteCache.get(color) ?? null
  const size = SPRITE_PX
  const made = offscreen(size, size)
  if (!made) {
    spriteCache.set(color, null)
    return null
  }
  const { canvas, ctx } = made
  const c = size / 2
  const glow = ctx.createRadialGradient(c, c, 0, c, c, c)
  glow.addColorStop(0, color)
  glow.addColorStop(0.14, color)
  glow.addColorStop(0.22, hexAlpha(color, 0.62))
  glow.addColorStop(0.52, hexAlpha(color, 0.2))
  glow.addColorStop(1, hexAlpha(color, 0))
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, size, size)
  // 核心：一小块纯亮，让星点在缩略尺寸下仍有实心感。
  ctx.beginPath()
  ctx.arc(c, c, size * 0.085, 0, Math.PI * 2)
  ctx.fillStyle = '#ffffff'
  ctx.globalAlpha = 0.85
  ctx.fill()
  spriteCache.set(color, canvas)
  return canvas
}

/** '#rrggbb' → 'rgba(r,g,b,a)'；非 hex（如 oklch）原样返回并靠 globalAlpha 兜底。 */
function hexAlpha(color: string, alpha: number): string {
  const m = /^#([0-9a-f]{6})$/i.exec(color.trim())
  if (!m) return color
  const v = parseInt(m[1], 16)
  return `rgba(${(v >> 16) & 255},${(v >> 8) & 255},${v & 255},${alpha})`
}

/**
 * 一帧该给哪些节点画光晕：先选中/悬停/命中，再方向与目标，最后事实；有上限。
 * 返回 Set 让节点循环里 O(1) 判断，不分配额外数组。
 */
export function glowIds(
  visible: ReadonlyArray<{ id: string; type: string }>,
  picks: { selected: string | null; hovered: string | null; matches: ReadonlySet<string> | null; keyboard: string | null },
  cap = MAX_GLOW,
): Set<string> {
  const out = new Set<string>()
  for (const n of visible) {
    if (n.id === picks.selected || n.id === picks.hovered || n.id === picks.keyboard || picks.matches?.has(n.id)) {
      out.add(n.id)
    }
  }
  for (const n of visible) {
    if (out.size >= cap) break
    if (n.type !== 'fact') out.add(n.id)
  }
  for (const n of visible) {
    if (out.size >= cap) break
    if (n.type === 'fact') out.add(n.id)
  }
  return out
}

/** 测试用：清掉精灵缓存（node 环境下没有 DOM，这里是空操作）。 */
export function resetStarfieldCache(): void {
  spriteCache.clear()
}
