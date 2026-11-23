// 星图观感：只改绘制层，布局、物理、命中测试、DOM 结构都不动。
//
// 用户要的是 EVE / 《群星》那种星空界面：暗场里有几团低透明度星云、远景星尘，
// 节点像星——核心亮点加一圈径向光晕，边像细亮的航道。这里的所有昂贵东西都预渲染：
// 光晕是离屏精灵（逐帧只 drawImage）、星云是一张离屏图、星尘是一张贴图，逐帧
// 不做 gradient 计算。减少动态效果时不做视差。
//
// 性能纪律：光晕只给「值得发光」的节点（选中/悬停/命中、方向与目标，以及少量
// 事实），一帧有上限；其余节点仍是亮点。563 节点下这条上限是每帧成本的关键。

/** 一帧最多画多少个光晕；超出的节点只画核心亮点。 */
export const MAX_GLOW = 160
/** 精灵的 CSS 边长：核心 + 外圈光晕。 */
const SPRITE_PX = 44
/** 星尘贴图边长。 */
const DUST_TILE = 160

export interface StarfieldView {
  x: number
  y: number
  k: number
}

interface LayerCache {
  nebula: HTMLCanvasElement | null
  dust: HTMLCanvasElement | null
}

const layers: LayerCache = { nebula: null, dust: null }
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
 * 星云：一张离屏图，4 团大的径向渐变。画的时候放大到视口以外再按相机做一点
 * 视差，所以平移时背景在动、又不至于露出边缘。
 */
function nebulaLayer(): HTMLCanvasElement | null {
  if (layers.nebula) return layers.nebula
  const size = 384
  const made = offscreen(size, size)
  if (!made) return null
  const { canvas, ctx } = made
  ctx.fillStyle = '#05070a'
  ctx.fillRect(0, 0, size, size)
  const blobs: Array<[number, number, number, string, number]> = [
    [0.28, 0.3, 0.52, '56,86,148', 0.5],
    [0.72, 0.24, 0.44, '104,64,138', 0.42],
    [0.62, 0.74, 0.5, '32,96,104', 0.34],
    [0.2, 0.82, 0.42, '124,74,56', 0.26],
  ]
  for (const [cx, cy, r, rgb, alpha] of blobs) {
    const x = cx * size
    const y = cy * size
    const radius = r * size
    const g = ctx.createRadialGradient(x, y, 0, x, y, radius)
    g.addColorStop(0, `rgba(${rgb},${alpha})`)
    g.addColorStop(0.45, `rgba(${rgb},${alpha * 0.45})`)
    g.addColorStop(1, `rgba(${rgb},0)`)
    ctx.fillStyle = g
    ctx.fillRect(x - radius, y - radius, radius * 2, radius * 2)
  }
  layers.nebula = canvas
  return canvas
}

/** 远景星尘：一张可平铺的小图，确定性生成（不摇随机数，截图才可复现）。 */
function dustLayer(): HTMLCanvasElement | null {
  if (layers.dust) return layers.dust
  const made = offscreen(DUST_TILE, DUST_TILE)
  if (!made) return null
  const { canvas, ctx } = made
  let seed = 1337
  const next = () => {
    seed = (seed * 1664525 + 1013904223) >>> 0
    return seed / 4294967296
  }
  for (let i = 0; i < 26; i += 1) {
    const x = Math.floor(next() * DUST_TILE)
    const y = Math.floor(next() * DUST_TILE)
    const bright = 0.10 + next() * 0.35
    const radius = next() < 0.15 ? 1.2 : 0.6
    ctx.fillStyle = `rgba(214,226,255,${bright})`
    ctx.beginPath()
    ctx.arc(x, y, radius, 0, Math.PI * 2)
    ctx.fill()
  }
  layers.dust = canvas
  return canvas
}

/**
 * 视差偏移（CSS px）。减少动态效果时恒为 0——「关掉视差」就在这里，用例直接断言
 * 它，不用做像素比对。
 */
export function parallaxOffset(cam: StarfieldView, motion: boolean): { x: number; y: number } {
  if (!motion) return { x: 0, y: 0 }
  return { x: cam.x * 0.02, y: cam.y * 0.02 }
}

/**
 * 画星空底：暗场渐变 → 星云（视差 2%）→ 星尘（视差 7%）。没有 DOM 时什么
 * 都不做，调用方保留原来的纯色底。
 */
export function paintStarfield(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  cam: StarfieldView,
  motion: boolean,
): boolean {
  const nebula = nebulaLayer()
  const dust = dustLayer()
  if (!nebula || !dust) return false

  const sky = ctx.createLinearGradient(0, 0, 0, height)
  sky.addColorStop(0, '#070a10')
  sky.addColorStop(0.55, '#05070b')
  sky.addColorStop(1, '#030407')
  ctx.fillStyle = sky
  ctx.fillRect(0, 0, width, height)

  // 视差用固定的最小步进，避免浮点噪声让背景每帧都动一点点。
  const { x: px, y: py } = parallaxOffset(cam, motion)
  const over = 1.35
  const nw = width * over
  const nh = height * over
  const nx = ((px % (nw - width)) + (nw - width)) % (nw - width) - (nw - width)
  const ny = ((py % (nh - height)) + (nh - height)) % (nh - height) - (nh - height)
  ctx.save()
  ctx.globalAlpha = 0.55
  ctx.drawImage(nebula, nx, ny, nw, nh)
  ctx.restore()

  const dx = motion ? cam.x * 0.07 : 0
  const dy = motion ? cam.y * 0.07 : 0
  const ox = ((dx % DUST_TILE) + DUST_TILE) % DUST_TILE
  const oy = ((dy % DUST_TILE) + DUST_TILE) % DUST_TILE
  const pattern = dustPattern(ctx, dust)
  if (pattern) {
    ctx.save()
    ctx.translate(ox - DUST_TILE, oy - DUST_TILE)
    ctx.fillStyle = pattern
    ctx.fillRect(0, 0, width + DUST_TILE * 2, height + DUST_TILE * 2)
    ctx.restore()
  }
  return true
}

const dustPatterns = new WeakMap<CanvasRenderingContext2D, CanvasPattern | null>()
function dustPattern(ctx: CanvasRenderingContext2D, tile: HTMLCanvasElement): CanvasPattern | null {
  if (dustPatterns.has(ctx)) return dustPatterns.get(ctx) ?? null
  let pattern: CanvasPattern | null = null
  try {
    pattern = ctx.createPattern(tile, 'repeat')
  } catch {
    pattern = null
  }
  dustPatterns.set(ctx, pattern)
  return pattern
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
    if (
      n.id === picks.selected ||
      n.id === picks.hovered ||
      n.id === picks.keyboard ||
      picks.matches?.has(n.id)
    ) {
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

/** 测试用：清掉离屏缓存（node 环境下没有 DOM，这里是空操作）。 */
export function resetStarfieldCache(): void {
  layers.nebula = null
  layers.dust = null
  spriteCache.clear()
}
