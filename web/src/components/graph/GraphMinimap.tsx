// 缩略图：整张图的俯视定位图 + 当前视口。
//
// 缩略图不承担图例职责。1100 个节点缩到一张小卡里时，逐点按种类上色、逐条画
// 产出边，只会得到一团彩色噪点（从远处看就像一台雷达），结构和位置都读不出来。
// 这里按信息层级重画：
//   - 事实/边界只作中性灰的密度底图；方向/目标保留状态色，已收口方向半透明；
//   - 大图不画边：上千条产出边加上百条前提边在小地图里只会变成放射噪点，关系
//     类型在 184px 上也读不出来；小图仍画全量边和事实颜色，此时它就是一张真正
//     的缩略图；
//   - 视口用暗角 + 白框圈出，卡片头给出“视口覆盖全图的比例”；
//   - 实时方向在它真实的位置上画呼吸环，不再有绕中心空转的扫描扇面。
//
// 相机由画布发布到 view-store（docs/33 §4.5）；这里用 useView 订阅，所以只有
// 订阅者随相机变化重渲染，图谱根组件不因平移/缩放而重渲染。点击或拖动把屏幕
// 坐标换算回世界坐标交给 onCenter（GraphPanel 接 canvas.centerOn）。
import { useEffect, useMemo, useRef, useState } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'
import type { EdgeKind, GraphModel, GraphNode } from '@/graph/model'
import { CANVAS, MATCH_RING, edgeStyle, nodeColor } from '@/graph/render'
import { useHovered, useView, type GraphView } from '@/graph/view-store'

/** CSS 尺寸固定：缩略图是参考物，不参与页面布局博弈。 */
const WIDTH = 184
const HEIGHT = 116
const PADDING = 7
/** 呼吸环的重绘间隔：30fps 足够，避免和主画布抢帧。 */
const PULSE_FRAME_MS = 33
/**
 * 超过这个节点数就切到“密度底图 + 骨架”模式：184px 宽放不下上千个彩色事实点，
 * 全画出来只会糊成一块色斑。小图仍然保留完整的上色和全部边。
 */
const DENSE_NODES = 240
/** 视口覆盖到这么多就算“看见全图”，不再画一个跑到画布外的框。 */
const FULL_VIEW = 0.97

const TAU = Math.PI * 2
const VIEWPORT_DIM = 'rgba(5,7,10,0.42)'
const VIEWPORT_FRAME = 'rgba(231,234,240,0.95)'
const FOCUS_RING = 'rgba(231,234,240,0.95)'

interface WorldBounds {
  minX: number
  minY: number
  maxX: number
  maxY: number
}

interface MinimapTransform {
  scale: number
  offsetX: number
  offsetY: number
  toWorldX: (px: number) => number
  toWorldY: (py: number) => number
}

interface MiniEdge {
  x1: number
  y1: number
  x2: number
  y2: number
  color: string
  alpha: number
}

/** 一帧要画的东西：分组/边在 useMemo 里重建，rAF 循环里只做坐标换算。 */
interface DrawScene {
  model: GraphModel
  bounds: WorldBounds
  viewport: { x0: number; y0: number; x1: number; y1: number }
  edges: MiniEdge[]
  dust: GraphNode[]
  marks: GraphNode[]
  active: GraphNode[]
  matched: GraphNode[]
  hovered: string | null
  /** 视口覆盖整张图的比例（0–1）；接近 1 时不再画视口框，免得框跑到画布外。 */
  coverage: number
  dense: boolean
  live: boolean
  motion: boolean
}

/** 节点在图坐标里的半尺寸；用绘制形状而非标签宽度，缩略图只需要大致轮廓。 */
function nodeHalf(n: GraphNode): { halfW: number; halfH: number } {
  if (n.type === 'fact' || n.type === 'ghost' || n.type === 'boundary') return { halfW: 5, halfH: 5 }
  if (n.type === 'step') return { halfW: 8, halfH: 8 }
  return { halfW: Math.max(n.w, 8), halfH: Math.max(n.h, 8) }
}

function worldBounds(model: GraphModel): WorldBounds | null {
  if (model.nodes.length === 0) return null
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const n of model.nodes) {
    const { halfW, halfH } = nodeHalf(n)
    minX = Math.min(minX, n.x - halfW)
    minY = Math.min(minY, n.y - halfH)
    maxX = Math.max(maxX, n.x + halfW)
    maxY = Math.max(maxY, n.y + halfH)
  }
  // A single node (or a stack at the same point) still needs a non-zero span.
  if (!(maxX > minX)) {
    minX -= 10
    maxX += 10
  }
  if (!(maxY > minY)) {
    minY -= 10
    maxY += 10
  }
  return { minX, minY, maxX, maxY }
}

/** 等比缩放并居中，世界坐标 → 缩略图像素；反向换算用于点击定位。 */
function minimapTransform(bounds: WorldBounds, width: number, height: number, pad: number): MinimapTransform {
  const spanX = bounds.maxX - bounds.minX
  const spanY = bounds.maxY - bounds.minY
  const innerW = Math.max(width - pad * 2, 1)
  const innerH = Math.max(height - pad * 2, 1)
  const scale = Math.min(innerW / spanX, innerH / spanY)
  const offsetX = pad + (innerW - spanX * scale) / 2
  const offsetY = pad + (innerH - spanY * scale) / 2
  return {
    scale,
    offsetX,
    offsetY,
    toWorldX: (px) => bounds.minX + (px - offsetX) / scale,
    toWorldY: (py) => bounds.minY + (py - offsetY) / scale,
  }
}

function clampToBounds(value: number, min: number, max: number): number {
  if (value < min) return min
  if (value > max) return max
  return value
}

/** 关系边的权重：骨架边比产出边长，产出边只在非密集图里出现。 */
function edgeAlpha(kind: EdgeKind): number {
  switch (kind) {
    case 'brief':
    case 'spawn':
    case 'goal':
    case 'supports':
      return 0.5
    case 'premise':
    case 'reference':
    case 'pin':
      return 0.4
    case 'host':
      return 0.3
    case 'origin':
      return 0.35
    default:
      return 0.24
  }
}

/** The camera's visible world rectangle (null until the canvas has a size). */
function viewportOf(view: GraphView): DrawScene['viewport'] | null {
  const { camera, width, height } = view
  if (width <= 0 || height <= 0 || !(camera.k > 0)) return null
  return {
    x0: -camera.x / camera.k,
    y0: -camera.y / camera.k,
    x1: (width - camera.x) / camera.k,
    y1: (height - camera.y) / camera.k,
  }
}

export function GraphMinimap({
  model,
  onCenter,
  live = false,
  matches = null,
}: {
  model: GraphModel
  onCenter: (worldX: number, worldY: number) => void
  /** 有方向在跑/复核：只在这时画呼吸环，静止时不保留常驻 rAF。 */
  live?: boolean
  /** 搜索命中的节点 id：在缩略图上圈出来，整图范围也能看到命中位置。 */
  matches?: ReadonlySet<string> | null
}) {
  const view = useView()
  const hovered = useHovered()
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const dragging = useRef(false)
  // 系统开启“减少动态效果”后，呼吸环停在第一帧，不再申请动画帧。
  const [motion, setMotion] = useState(true)

  const bounds = useMemo(() => worldBounds(model), [model])
  const dense = model.nodes.length > DENSE_NODES
  const viewport = useMemo(() => viewportOf(view), [view])

  // 边只保留模型里两端都还在的那些；模型重建时才重算，平移/缩放不重新分配。
  // 大图不画边：上千条产出边加上百条前提边在小地图里只会变成放射噪点，关系
  // 类型在 184px 上根本读不出来；密度和方向落点已经能说明“图长什么样”。
  const edges = useMemo(() => {
    if (dense) return []
    const list: MiniEdge[] = []
    for (const e of model.edges) {
      const s = model.byId.get(e.source)
      const t = model.byId.get(e.target)
      if (!s || !t) continue
      list.push({
        x1: s.x,
        y1: s.y,
        x2: t.x,
        y2: t.y,
        color: edgeStyle(e).color,
        alpha: edgeAlpha(e.kind),
      })
    }
    return list
  }, [model, dense])

  // 事实是底图，方向/目标/项目是骨架：分层画，骨架永远压在密度上面。
  const { dust, marks } = useMemo(() => {
    const dustNodes: GraphNode[] = []
    const markNodes: GraphNode[] = []
    for (const n of model.nodes) {
      if (n.type === 'fact' || n.type === 'ghost' || n.type === 'boundary') dustNodes.push(n)
      else markNodes.push(n)
    }
    return { dust: dustNodes, marks: markNodes }
  }, [model])

  // 实时方向：步骤节点（ghost 是同一方向的未产出端，不重复画环）。
  const active = useMemo(
    () => marks.filter((n) => n.type === 'step' && (n.status === 'running' || n.status === 'reviewing')),
    [marks],
  )
  const matched = useMemo(
    () => (matches ? model.nodes.filter((n) => matches.has(n.id)) : []),
    [model, matches],
  )

  // 视口覆盖比例：不是缩放倍数，而是“当前画面看到了整张图的多少”，操作员更
  // 容易判断自己是在看局部还是全貌。相机移出图外时诚实显示 0%。
  const coverage = useMemo(() => {
    if (!bounds || !viewport) return 0
    const ix = Math.max(
      0,
      Math.min(viewport.x1, bounds.maxX) - Math.max(viewport.x0, bounds.minX),
    )
    const iy = Math.max(
      0,
      Math.min(viewport.y1, bounds.maxY) - Math.max(viewport.y0, bounds.minY),
    )
    const spanX = bounds.maxX - bounds.minX
    const spanY = bounds.maxY - bounds.minY
    if (!(spanX > 0) || !(spanY > 0)) return 0
    return Math.min(1, (ix * iy) / (spanX * spanY))
  }, [bounds, viewport])
  const coveragePct = coverage > 0 ? Math.max(1, Math.round(coverage * 100)) : 0

  const scene: DrawScene | null =
    bounds && viewport
      ? { model, bounds, viewport, edges, dust, marks, active, matched, hovered, coverage, dense, live, motion }
      : null
  const sceneRef = useRef<DrawScene | null>(null)
  sceneRef.current = scene

  // 画布：骨架边 → 事实密度 → 方向/目标 → 视口暗角与白框 → 命中/悬停/实时环。
  const draw = (now: number) => {
    const canvas = canvasRef.current
    const current = sceneRef.current
    if (!canvas || !current) return
    const dpr = typeof window !== 'undefined' && window.devicePixelRatio > 0 ? window.devicePixelRatio : 1
    const pixelW = Math.round(WIDTH * dpr)
    const pixelH = Math.round(HEIGHT * dpr)
    if (canvas.width !== pixelW) canvas.width = pixelW
    if (canvas.height !== pixelH) canvas.height = pixelH
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, WIDTH, HEIGHT)

    const map = minimapTransform(current.bounds, WIDTH, HEIGHT, PADDING)
    const xOf = (x: number) => map.offsetX + (x - current.bounds.minX) * map.scale
    const yOf = (y: number) => map.offsetY + (y - current.bounds.minY) * map.scale
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'

    // 1. 关系骨架：只有小图才画边；大图的边见 edges memo，全部跳过。
    ctx.lineWidth = 0.7
    for (const e of current.edges) {
      ctx.globalAlpha = e.alpha
      ctx.strokeStyle = e.color
      ctx.beginPath()
      ctx.moveTo(xOf(e.x1), yOf(e.y1))
      ctx.lineTo(xOf(e.x2), yOf(e.y2))
      ctx.stroke()
    }

    // 2. 事实（以及边界/未产出端）：密集图里是中性灰的密度底图，小图里保留种类色。
    //    密集图的样式是常量，必须提到循环外：1100 次 fillStyle/globalAlpha 赋值
    //    会在首帧堆出几十毫秒的 long task，而 1100 个 1px 方块本身并不慢。
    if (current.dense) {
      ctx.globalAlpha = 0.42
      ctx.fillStyle = CANVAS.ink3
      for (const n of current.dust) {
        ctx.fillRect(xOf(n.x) - 0.7, yOf(n.y) - 0.7, 1.4, 1.4)
      }
    } else {
      for (const n of current.dust) {
        ctx.globalAlpha = n.type === 'ghost' ? 0.6 : 0.9
        ctx.fillStyle = nodeColor(n)
        ctx.fillRect(xOf(n.x) - 1, yOf(n.y) - 1, 2, 2)
      }
    }

    // 3. 方向/目标/项目。大图里已收口（done/parked）的方向退成半透明，只让还在
    //    跑、待处理或失败的方向保持高对比：既看得到方向的分布，又不会被一片完成
    //    状态的色块淹没；小图信息量小，全部保留完整状态色。
    for (const n of current.marks) {
      const x = xOf(n.x)
      const y = yOf(n.y)
      if (n.type === 'project') {
        ctx.globalAlpha = 0.9
        ctx.fillStyle = nodeColor(n)
        ctx.fillRect(x - 2.2, y - 2.2, 4.4, 4.4)
        continue
      }
      const quiet = current.dense && (n.status === 'done' || n.status === 'parked')
      ctx.globalAlpha = quiet ? 0.42 : 0.95
      ctx.fillStyle = nodeColor(n)
      const r = n.type === 'goal' ? 3.2 : quiet ? 1.9 : 2.4
      ctx.beginPath()
      ctx.moveTo(x, y - r)
      ctx.lineTo(x + r, y)
      ctx.lineTo(x, y + r)
      ctx.lineTo(x - r, y)
      ctx.closePath()
      ctx.fill()
    }

    // 4. 视口：外面压暗、里面留亮，再描一圈白框。视口已经罩住几乎整张图时
    //    不画框（否则框会跑到画布外，只剩一条莫名其妙的边）；视口被画布裁掉
    //    时用虚线框，明确“它还在画布之外继续”。
    if (current.viewport && current.coverage < FULL_VIEW) {
      const vx = current.viewport
      const rawX = xOf(vx.x0)
      const rawY = yOf(vx.y0)
      const rawW = (vx.x1 - vx.x0) * map.scale
      const rawH = (vx.y1 - vx.y0) * map.scale
      const rx = Math.max(0, Math.min(WIDTH, rawX))
      const ry = Math.max(0, Math.min(HEIGHT, rawY))
      const rw = Math.max(Math.max(0, Math.min(WIDTH, rawX + rawW)) - rx, 2)
      const rh = Math.max(Math.max(0, Math.min(HEIGHT, rawY + rawH)) - ry, 2)
      const clipped = rawX < 0 || rawY < 0 || rawX + rawW > WIDTH || rawY + rawH > HEIGHT
      ctx.save()
      ctx.beginPath()
      ctx.rect(0, 0, WIDTH, HEIGHT)
      ctx.rect(rx, ry, rw, rh)
      ctx.fillStyle = VIEWPORT_DIM
      ctx.fill('evenodd')
      ctx.restore()
      ctx.globalAlpha = 1
      ctx.fillStyle = 'rgba(231,234,240,0.05)'
      ctx.fillRect(rx, ry, rw, rh)
      // 深色垫底再描亮框，压得住下面的灰色密度。
      if (clipped) ctx.setLineDash([3, 3])
      ctx.lineWidth = 2.4
      ctx.strokeStyle = 'rgba(5,7,10,0.8)'
      ctx.strokeRect(rx + 0.5, ry + 0.5, rw, rh)
      ctx.lineWidth = 1
      ctx.strokeStyle = VIEWPORT_FRAME
      ctx.strokeRect(rx + 0.5, ry + 0.5, rw, rh)
      ctx.setLineDash([])
    }

    // 5. 搜索命中：红圈；悬停：白圈；实时方向：状态色的呼吸环。
    if (current.matched.length > 0) {
      ctx.lineWidth = 1
      ctx.strokeStyle = MATCH_RING
      for (const n of current.matched) {
        ctx.beginPath()
        ctx.arc(xOf(n.x), yOf(n.y), 3.2, 0, TAU)
        ctx.stroke()
      }
    }
    if (current.hovered) {
      const n = current.model.byId.get(current.hovered)
      if (n) {
        ctx.lineWidth = 1.2
        ctx.strokeStyle = FOCUS_RING
        ctx.beginPath()
        ctx.arc(xOf(n.x), yOf(n.y), 4, 0, TAU)
        ctx.stroke()
      }
    }
    if (current.live) {
      for (const n of current.active) {
        const pulse = current.motion ? (Math.sin(now / 520) + 1) / 2 : 0.55
        const color = nodeColor(n)
        ctx.globalAlpha = 0.28 + pulse * 0.45
        ctx.lineWidth = 1.2
        ctx.strokeStyle = color
        ctx.beginPath()
        ctx.arc(xOf(n.x), yOf(n.y), 3 + pulse * 2.4, 0, TAU)
        ctx.stroke()
        ctx.globalAlpha = 0.95
        ctx.fillStyle = color
        ctx.beginPath()
        ctx.arc(xOf(n.x), yOf(n.y), 1.6, 0, TAU)
        ctx.fill()
      }
      ctx.globalAlpha = 1
    }
  }
  const drawRef = useRef(draw)
  drawRef.current = draw

  // 数据/相机/悬停/实时状态变化：立即重画一帧。`live` 与 `motion` 也在这里：
  // 关掉实时或系统减少动态效果时必须补画一帧，否则上一帧的呼吸环会留在画布上。
  useEffect(() => {
    drawRef.current(typeof performance !== 'undefined' ? performance.now() : 0)
  }, [bounds, model, view, live, motion, hovered, matches])

  // prefers-reduced-motion 是无障碍设置：环仍画出来，但不再转动。
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const sync = () => setMotion(!mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])

  // 只在 live 且允许动效时申请动画帧；静止/卸载即停，不留常驻 rAF。
  useEffect(() => {
    if (!live || !motion || typeof requestAnimationFrame !== 'function') return
    let raf = 0
    let last = 0
    const loop = (now: number) => {
      raf = requestAnimationFrame(loop)
      if (now - last < PULSE_FRAME_MS) return
      last = now
      drawRef.current(now)
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [live, motion])

  if (!bounds || view.width <= 0 || view.height <= 0) return null

  const point = (event: ReactPointerEvent<HTMLCanvasElement>) => {
    const map = minimapTransform(bounds, WIDTH, HEIGHT, PADDING)
    const rect = event.currentTarget.getBoundingClientRect()
    const px = ((event.clientX - rect.left) * WIDTH) / Math.max(rect.width, 1)
    const py = ((event.clientY - rect.top) * HEIGHT) / Math.max(rect.height, 1)
    onCenter(
      clampToBounds(map.toWorldX(px), bounds.minX, bounds.maxX),
      clampToBounds(map.toWorldY(py), bounds.minY, bounds.maxY),
    )
  }

  return (
    <div
      className="pointer-events-auto overflow-hidden rounded-xl border border-hairline bg-card/90 shadow-lifted backdrop-blur"
      title={
        dense
          ? '缩略图：灰色是事实密度，菱形是方向/目标（已收口方向半透明）；点击或拖动定位视图'
          : '缩略图：颜色是节点种类与方向状态，白框是当前视口；点击或拖动定位视图'
      }
    >
      <div className="flex items-center justify-between gap-2 px-2 pt-1.5 pb-1">
        <span className="text-[10px] font-medium tracking-wide text-ink-3">全图缩略</span>
        <span
          className="tw-nums font-mono text-[10px] text-ink-3"
          title="当前视口覆盖整张图的比例；点按缩略图可把视图移过去"
        >
          视口 {coveragePct}%
        </span>
      </div>
      <canvas
        ref={canvasRef}
        data-testid="graph-minimap"
        aria-label="图谱缩略图：显示整张图的节点与当前视口，点击或拖动可定位视图"
        className="block h-[116px] w-[184px] cursor-crosshair"
        style={{ width: WIDTH, height: HEIGHT }}
        onPointerDown={(event) => {
          event.preventDefault()
          dragging.current = true
          try {
            event.currentTarget.setPointerCapture(event.pointerId)
          } catch {
            /* 老浏览器不支持捕获也不影响单击定位 */
          }
          point(event)
        }}
        onPointerMove={(event) => {
          if (!dragging.current) return
          event.preventDefault()
          point(event)
        }}
        onPointerUp={(event) => {
          dragging.current = false
          try {
            event.currentTarget.releasePointerCapture(event.pointerId)
          } catch {
            /* 已释放 */
          }
        }}
        onPointerCancel={() => {
          dragging.current = false
        }}
      />
    </div>
  )
}
