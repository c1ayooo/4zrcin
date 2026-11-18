// 缩略图：整张图的极简俯视图 + 当前视口矩形。
//
// 画布把相机发布到 view-store（docs/33 §4.5）；这里用 useView 订阅，所以只有
// 订阅者随相机变化重渲染，图谱根组件不因平移/缩放而重渲染。点击或拖动把
// 屏幕坐标换算回世界坐标交给 onCenter（GraphPanel 接 canvas.centerOn）。
//
// 动效：`live` 为真（有方向在跑/在复核）时叠一圈慢速扫描扇面，让“实时态势”
// 在小地图上也可见。它只在 live 时开 rAF，且尊重 prefers-reduced-motion：静止
// 状态下不保留循环，避免一个永远空转的动画。
import { useEffect, useMemo, useRef } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'
import type { GraphModel, GraphNode } from '@/graph/model'
import { nodeColor } from '@/graph/render'
import { useView } from '@/graph/view-store'

/** CSS 尺寸固定：缩略图是参考物，不参与页面布局博弈。 */
const WIDTH = 168
const HEIGHT = 112
const PADDING = 6
/** 扫描扇面的重绘间隔：30fps 足够，避免和主画布抢帧。 */
const SWEEP_FRAME_MS = 33

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

/** 节点在图坐标里的半尺寸；用绘制形状而非标签宽度，缩略图只需要大致轮廓。 */
function nodeHalf(n: GraphNode): { halfW: number; halfH: number } {
  if (n.type === 'fact' || n.type === 'ghost') return { halfW: 5, halfH: 5 }
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

export function GraphMinimap({
  model,
  onCenter,
  live = false,
}: {
  model: GraphModel
  onCenter: (worldX: number, worldY: number) => void
  /** 有方向在跑/复核：只在这时画扫描扇面，静止时不留空转动画。 */
  live?: boolean
}) {
  const view = useView()
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const dragging = useRef(false)
  const bounds = useMemo(() => worldBounds(model), [model])
  // 帧循环读取最新的模型/相机/边界；把它们放进 ref，避免每次数据变化都重建循环。
  const modelRef = useRef(model)
  modelRef.current = model
  const viewRef = useRef(view)
  viewRef.current = view
  const boundsRef = useRef(bounds)
  boundsRef.current = bounds
  const motionRef = useRef(true)
  const drawRef = useRef<(now: number) => void>(() => {})

  // 画布：节点缩略 + 视口矩形 +（live 时）扫描扇面。相机/模型变化时立即重画。
  const draw = (now: number) => {
    const canvas = canvasRef.current
    const current = boundsRef.current
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
    const map = minimapTransform(current, WIDTH, HEIGHT, PADDING)

    for (const n of modelRef.current.nodes) {
      const px = map.offsetX + (n.x - current.minX) * map.scale
      const py = map.offsetY + (n.y - current.minY) * map.scale
      ctx.globalAlpha = n.type === 'project' ? 0.95 : 0.68
      ctx.fillStyle = nodeColor(n)
      ctx.fillRect(px - 1, py - 1, 2.2, 2.2)
    }
    ctx.globalAlpha = 1

    // 扫描扇面：只有实时态势（有方向在跑/复核）才转动，且尊重 reduced motion。
    if (live && motionRef.current) {
      const cx = WIDTH / 2
      const cy = HEIGHT / 2
      const radius = Math.min(WIDTH, HEIGHT) * 0.46
      const angle = (now / 1000) * 0.9 - Math.PI / 2
      ctx.globalAlpha = 0.1
      ctx.beginPath()
      ctx.moveTo(cx, cy)
      ctx.arc(cx, cy, radius, angle - 0.55, angle)
      ctx.closePath()
      ctx.fillStyle = 'rgba(61,214,140,1)'
      ctx.fill()
      ctx.globalAlpha = 0.42
      ctx.beginPath()
      ctx.moveTo(cx, cy)
      ctx.lineTo(cx + Math.cos(angle) * radius, cy + Math.sin(angle) * radius)
      ctx.strokeStyle = 'rgba(61,214,140,1)'
      ctx.lineWidth = 1.1
      ctx.stroke()
      ctx.globalAlpha = 0.18
      ctx.beginPath()
      ctx.arc(cx, cy, radius * 0.96, 0, Math.PI * 2)
      ctx.setLineDash([3, 5])
      ctx.strokeStyle = 'rgba(61,214,140,1)'
      ctx.lineWidth = 1
      ctx.stroke()
      ctx.setLineDash([])
      ctx.globalAlpha = 1
    }

    // 视口矩形：相机把世界坐标映射为 screen = world * k + cam。
    const cam = viewRef.current.camera
    const viewSize = viewRef.current
    if (viewSize.width > 0 && viewSize.height > 0 && cam.k > 0) {
      const x0 = (0 - cam.x) / cam.k
      const y0 = (0 - cam.y) / cam.k
      const x1 = (viewSize.width - cam.x) / cam.k
      const y1 = (viewSize.height - cam.y) / cam.k
      const rx = map.offsetX + (x0 - current.minX) * map.scale
      const ry = map.offsetY + (y0 - current.minY) * map.scale
      const rw = (x1 - x0) * map.scale
      const rh = (y1 - y0) * map.scale
      ctx.lineWidth = 1
      // ink-1 at 85%: the viewport rectangle is chrome, not a semantic colour.
      ctx.strokeStyle = 'rgba(231,234,240,0.85)'
      ctx.strokeRect(rx + 0.5, ry + 0.5, Math.max(rw, 2), Math.max(rh, 2))
    }
  }
  drawRef.current = draw

  // 数据/相机/实时状态变化：立即重画一帧。`live` 也在这里：扫描扇面只属于实时
  // 帧，翻成 false 时必须补画一帧，否则上一帧的扇面和虚线环会一直留在画布上，
  // 直到用户平移/缩放（相机不变时可能永远不刷新）。
  useEffect(() => {
    drawRef.current(typeof performance !== 'undefined' ? performance.now() : 0)
  }, [bounds, model, view, live])

  // prefers-reduced-motion 是无障碍设置：开启后保留扫描环，但不再转动。
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const sync = () => {
      motionRef.current = !mq.matches
      drawRef.current(typeof performance !== 'undefined' ? performance.now() : 0)
    }
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])

  // live 时转动扫描扇面；静止/卸载即停，不留常驻 rAF。
  useEffect(() => {
    if (!live || typeof requestAnimationFrame !== 'function') return
    let raf = 0
    let last = 0
    const loop = (now: number) => {
      raf = requestAnimationFrame(loop)
      if (!motionRef.current || now - last < SWEEP_FRAME_MS) return
      last = now
      drawRef.current(now)
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [live])

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
      className="pointer-events-auto overflow-hidden rounded-xl border border-hairline bg-card/90 p-1 shadow-lifted backdrop-blur"
      title="缩略图：点击或拖动定位到图上的位置"
    >
      <canvas
        ref={canvasRef}
        data-testid="graph-minimap"
        aria-label="图谱缩略图，点击或拖动可定位视图"
        className="block h-[112px] w-[168px] cursor-crosshair rounded-md"
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
