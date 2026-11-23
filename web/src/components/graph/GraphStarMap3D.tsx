// 3D 星图：有结构的三维地图。
//
// 渲染壳为什么是 three + 它自带的 OrbitControls，而不是 three-forcegraph /
// three-render-objects：那两个库在本页面上渲染不出任何东西——场景可见、图对象已挂、
// 相机矩阵有限，但 `renderer.info.render.calls` 与 `memory.geometries` 都是 0
// （诊断读数留在 `_research/star-map-3d-integration.md`）。既然它们连"画出来"这
// 一步都没做到，就退回 three 本身：three 是依赖、OrbitControls 是 three 自带 addon，
// 我们只写产品特有的两层——分层 z（graph3d/layers.ts）和观感（星点光晕 + 弧线航道）。
//
// 第三轴是结构：项目/目标在上、方向居中、事实在下（x/y 沿用 2D 力导向），不跑三维
// 力导向。找节点靠搜索：选中后镜头直接对准它，不用转着找；找链仍回 2D 链路树。
import { useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import type { GraphModel } from '@/graph/model'
import { edgeStyle, nodeColor } from '@/graph/render'
import { ForceSim } from '@/graph3d/sim3d'

const ARC_SEGMENTS = 5
const HOME = new THREE.Vector3(0, -900, 700)
// 3D 里画全部边：我们是一条 LineSegments、单批几何、单次 draw call，所以 798 条边\n// 只是一块 buffer，不存在「每边一个 Line」的 draw call 问题。骨架过滤反而会让\n// 75% 的事实变成孤立点（反方 实测 421/559），看着又像星云。

function starTexture(): THREE.Texture | null {
  if (typeof document === 'undefined') return null
  const size = 64
  const canvas = document.createElement('canvas')
  canvas.width = size
  canvas.height = size
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  const c = size / 2
  const glow = ctx.createRadialGradient(c, c, 0, c, c, c)
  glow.addColorStop(0, 'rgba(255,255,255,1)')
  glow.addColorStop(0.16, 'rgba(255,255,255,0.85)')
  glow.addColorStop(0.42, 'rgba(255,255,255,0.22)')
  glow.addColorStop(1, 'rgba(255,255,255,0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, size, size)
  const texture = new THREE.CanvasTexture(canvas)
  texture.needsUpdate = true
  return texture
}

function bezier(a: THREE.Vector3, m: THREE.Vector3, b: THREE.Vector3, t: number, out: THREE.Vector3) {
  const u = 1 - t
  out.set(
    u * u * a.x + 2 * u * t * m.x + t * t * b.x,
    u * u * a.y + 2 * u * t * m.y + t * t * b.y,
    u * u * a.z + 2 * u * t * m.z + t * t * b.z,
  )
  return out
}

export function GraphStarMap3D({
  model,
  selected,
  matches = null,
  onPick,
}: {
  model: GraphModel
  selected?: string | null
  matches?: ReadonlySet<string> | null
  onPick: (id: string) => void
}) {
  const boxRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const pickRef = useRef(onPick)
  pickRef.current = onPick
  const focusRef = useRef<((id: string) => void) | null>(null)
  // WebGL 上下文丢失：给一句提示，恢复后自动重画（浏览器会自己重建上下文）。
  const [contextLost, setContextLost] = useState(false)
  const selectedRef = useRef<string | null>(selected ?? null)
  selectedRef.current = selected ?? null

  useEffect(() => {
    const box = boxRef.current
    const canvas = canvasRef.current
    if (!box || !canvas) return
    if (box.clientWidth < 8 || box.clientHeight < 8) return

    const sim = new ForceSim(model)
    const nodes = sim.nodes
    const index = new Map(nodes.map((node, i) => [node.id, i]))
    const edges: Array<[number, number]> = []
    for (const edge of model.edges) {
      const a = index.get(edge.source)
      const b = index.get(edge.target)
      if (a !== undefined && b !== undefined) edges.push([a, b])
    }

    const renderer = new THREE.WebGLRenderer({ canvas, antialias: false, alpha: false })
    // 3D 是概览视图：1x 像素比 + 5 段弧就够，2x 会让拖动帧成本翻倍到 4.8ms。
    renderer.setPixelRatio(1)
    // 3D 用和 2D 同一个底色（CANVAS.base），不再带蓝调、也不铺星云。
    renderer.setClearColor(0x0a0c10, 1)
    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(48, 1, 1, 200000)
    camera.position.copy(HOME)
    const controls = new OrbitControls(camera, canvas)
    controls.target.set(0, 0, 0)
    // 用户要「转动拖动穿梭」：允许平移，才能贴近看某一块。
    controls.enablePan = true
    controls.enableDamping = false
    controls.rotateSpeed = 0.75
    controls.zoomSpeed = 0.9
    // 俯仰夹 ±85°：越过极点画面会翻转，操作员会失去上下方向。
    controls.minPolarAngle = (5 * Math.PI) / 180
    controls.maxPolarAngle = Math.PI - (5 * Math.PI) / 180
    controls.minDistance = 200
    controls.maxDistance = 20000

    const texture = starTexture()
    const positions = new Float32Array(nodes.length * 3)
    const MAX_CHANNEL = 0.9 // 亮度封顶，见 pointMaterial 处注释
  const baseColors = new Float32Array(nodes.length * 3)
    const colors = new Float32Array(nodes.length * 3)
    const color = new THREE.Color()
    model.nodes.forEach((node, i) => {
      color.set(nodeColor(node))
      baseColors[i * 3] = Math.min(color.r, MAX_CHANNEL)
      baseColors[i * 3 + 1] = Math.min(color.g, MAX_CHANNEL)
      baseColors[i * 3 + 2] = Math.min(color.b, MAX_CHANNEL)
    })
    colors.set(baseColors)
    const pointGeometry = new THREE.BufferGeometry()
    pointGeometry.setAttribute('position', new THREE.BufferAttribute(positions, 3))
    pointGeometry.setAttribute('color', new THREE.BufferAttribute(colors, 3))
    const pointMaterial = new THREE.PointsMaterial({
      // 像素尺寸、关闭距离衰减：图跨几千个世界单位，按世界尺寸画的点会小到看不见。
      size: 7,
      map: texture ?? undefined,
      vertexColors: true,
      // 普通混合 + 亮度封顶（MAX_CHANNEL）：加色混合下几百条航道汇到同一处会烧成
      // 一片纯白（反方 量到 ≥250 的像素占 3.99%）；封顶后叠多少层都停在 230 以下。
      transparent: true,
      opacity: 0.92,
      depthWrite: false,
      sizeAttenuation: false,
    })
    const points = new THREE.Points(pointGeometry, pointMaterial)
    scene.add(points)

    const segs = edges.length * ARC_SEGMENTS
    const linePositions = new Float32Array(segs * 6)
    const baseLineColors = new Float32Array(edges.length * 3)
    const lineColors = new Float32Array(segs * 6)
    const lineGeometry = new THREE.BufferGeometry()
    lineGeometry.setAttribute('position', new THREE.BufferAttribute(linePositions, 3))
    lineGeometry.setAttribute('color', new THREE.BufferAttribute(lineColors, 3))
    const lineMaterial = new THREE.LineBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: 0.3,
      depthWrite: false,
    })
    const lines = new THREE.LineSegments(lineGeometry, lineMaterial)
    scene.add(lines)

    // 边色：按 kind 取同一套配色。
    const edgeColor = new THREE.Color()
    {
      let e = 0
      for (const edge of model.edges) {
        if (!index.has(edge.source) || !index.has(edge.target)) continue
        edgeColor.set(edgeStyle(edge).color)
        baseLineColors[e * 3] = Math.min(edgeColor.r, MAX_CHANNEL)
        baseLineColors[e * 3 + 1] = Math.min(edgeColor.g, MAX_CHANNEL)
        baseLineColors[e * 3 + 2] = Math.min(edgeColor.b, MAX_CHANNEL)
        e += 1
      }
    }

    const tmpA = new THREE.Vector3()
    const tmpM = new THREE.Vector3()
    const tmpB = new THREE.Vector3()
    const tmpP0 = new THREE.Vector3()
    const tmpP1 = new THREE.Vector3()
    let zMin = 0
    let zMax = 1
    const syncPositions = () => {
      zMin = Infinity
      zMax = -Infinity
      for (let i = 0; i < nodes.length; i += 1) {
        positions[i * 3] = nodes[i].x
        positions[i * 3 + 1] = nodes[i].y
        positions[i * 3 + 2] = nodes[i].z
        if (nodes[i].z < zMin) zMin = nodes[i].z
        if (nodes[i].z > zMax) zMax = nodes[i].z
      }
      pointGeometry.getAttribute('position').needsUpdate = true
      // 坐标每帧在变，包围球必须跟着重算：three 只在为 null 时算一次，
      // 旧球停在初始螺旋上，射线会先被球面判定挡掉（点选全空的根因）。
      pointGeometry.computeBoundingSphere()
      let w = 0
      for (const [a, b] of edges) {
        tmpA.set(nodes[a].x, nodes[a].y, nodes[a].z)
        tmpB.set(nodes[b].x, nodes[b].y, nodes[b].z)
        tmpM.copy(tmpA).add(tmpB).multiplyScalar(0.5)
        tmpM.z += 90
        for (let s = 0; s < ARC_SEGMENTS; s += 1) {
          bezier(tmpA, tmpM, tmpB, s / ARC_SEGMENTS, tmpP0)
          bezier(tmpA, tmpM, tmpB, (s + 1) / ARC_SEGMENTS, tmpP1)
          linePositions[w] = tmpP0.x
          linePositions[w + 1] = tmpP0.y
          linePositions[w + 2] = tmpP0.z
          linePositions[w + 3] = tmpP1.x
          linePositions[w + 4] = tmpP1.y
          linePositions[w + 5] = tmpP1.z
          w += 6
        }
      }
      lineGeometry.getAttribute('position').needsUpdate = true
    }

    /** 选中/命中的强调：邻域亮、其余压暗；没选中时按层深衰减。 */
    const highlight = (selectedId: string | null, matchSet: ReadonlySet<string> | null) => {
      const focus = selectedId ? index.get(selectedId) : undefined
      const neighbours = new Set<number>()
      if (focus !== undefined) {
        neighbours.add(focus)
        for (const [a, b] of edges) {
          if (a === focus || b === focus) {
            neighbours.add(a)
            neighbours.add(b)
          }
        }
      }
      for (let i = 0; i < nodes.length; i += 1) {
        let k = 1
        if (focus !== undefined) k = neighbours.has(i) ? 1 : 0.16
        if (matchSet?.has(nodes[i].id)) k = Math.min(1.2, k + 0.45)
        colors[i * 3] = baseColors[i * 3] * k
        colors[i * 3 + 1] = baseColors[i * 3 + 1] * k
        colors[i * 3 + 2] = baseColors[i * 3 + 2] * k
      }
      pointGeometry.getAttribute('color').needsUpdate = true
      let w = 0
      edges.forEach(([a, b], edgeIndex) => {
        const incident = focus !== undefined && (a === focus || b === focus)
        // 层深衰减按实际 z 跨度归一：写死 420 在自适应层高下会算出 >1 的系数，
        // 把高层的航道顶到溢出（这也是纯白像素的一个来源）。
        const depth = 0.4 + 0.6 * (((nodes[a].z + nodes[b].z) / 2 - zMin) / Math.max(zMax - zMin, 1))
        const k = focus === undefined ? depth : incident ? 1.2 : 0.08
        const r = baseLineColors[edgeIndex * 3] * k
        const g = baseLineColors[edgeIndex * 3 + 1] * k
        const bl = baseLineColors[edgeIndex * 3 + 2] * k
        for (let s = 0; s < ARC_SEGMENTS * 2; s += 1) {
          lineColors[w] = r
          lineColors[w + 1] = g
          lineColors[w + 2] = bl
          w += 3
        }
      })
      lineGeometry.getAttribute('color').needsUpdate = true
    }

    // 用户是否动过相机：动过之后一律不再自动拟合，直到双击回正。
    // 否则「放大贴近某个节点」会在 2D 收敛期间被每 15 帧一次的取景拽回全局。
    let userMoved = false

    // 相机按分位包围盒拟合：真实数据里有跨几万个世界单位的离群点，用 min/max 会把
    // 主体压成一条线（反方 量到的 740×40 刀片就是这个）；取 2%–98% 分位，只丢两端尾巴。
    const quantile = (values: number[], q: number) => {
      const sorted = [...values].sort((a, b) => a - b)
      return sorted[Math.min(sorted.length - 1, Math.max(0, Math.floor((sorted.length - 1) * q)))] ?? 0
    }
    const frameCamera = () => {
      const xs = nodes.map((n) => n.x)
      const ys = nodes.map((n) => n.y)
      const zs = nodes.map((n) => n.z)
      const lo = [quantile(xs, 0.02), quantile(ys, 0.02), quantile(zs, 0.02)]
      const hi = [quantile(xs, 0.98), quantile(ys, 0.98), quantile(zs, 0.98)]
      const center = new THREE.Vector3((lo[0] + hi[0]) / 2, (lo[1] + hi[1]) / 2, (lo[2] + hi[2]) / 2)
      const span = [hi[0] - lo[0], hi[1] - lo[1], hi[2] - lo[2]]
      const size = Math.max(Math.hypot(span[0], span[1], span[2]), 600)
      const fov = (camera.fov * Math.PI) / 180
      // 视线方向固定成俯角，只有距离由投影反推，避免再堆一个拍出来的系数。
      const dir = new THREE.Vector3(0, -0.72, 0.66).normalize()
      const place = (d: number) => {
        camera.position.copy(center).addScaledVector(dir, d)
        camera.near = Math.max(1, d / 5000)
        camera.far = Math.max(50000, d * 8)
        controls.target.copy(center)
        controls.minDistance = d * 0.15
        controls.maxDistance = d * 4
        camera.lookAt(center)
        camera.updateProjectionMatrix()
        camera.updateMatrixWorld()
      }
      // 投影反推：把最外点放到画面 82%（== 四边各留 9% 画幅）。写死系数时
      // 实际距离是 0.976d、又是按包围盒对角算的，两处近似叠起来顶部只剩 8px。
      const probe = new THREE.Vector3()
      let distance = size / 2 / Math.tan(fov / 2)
      for (let pass = 0; pass < 3; pass += 1) {
        place(distance)
        let maxAbs = 0
        for (const node of nodes) {
          probe.set(node.x, node.y, node.z).project(camera)
          if (probe.z >= 1) continue
          const reach = Math.max(Math.abs(probe.x), Math.abs(probe.y))
          if (reach > maxAbs) maxAbs = reach
        }
        if (!(maxAbs > 1e-6)) break
        distance *= maxAbs / 0.82
      }
      place(distance)
      controls.update()
    }

    const resize = () => {
      const width = Math.max(box.clientWidth, 1)
      const height = Math.max(box.clientHeight, 1)
      camera.aspect = width / height
      camera.updateProjectionMatrix()
      renderer.setSize(width, height, false)
      render()
    }
    const render = () => renderer.render(scene, camera)

    const perf = { first: 0, frames: [] as number[] }
    let lastPublish = 0
    const publish = () => {
      const sorted = [...perf.frames].sort((a, b) => a - b)
      const at = (q: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] ?? 0
      canvas.dataset.frameMs = JSON.stringify({
        n: sorted.length,
        first: Math.round(perf.first * 100) / 100,
        p50: Math.round(at(0.5) * 100) / 100,
        p95: Math.round(at(0.95) * 100) / 100,
      })
    }
    canvas.dataset.nodes = String(nodes.length)
    canvas.dataset.edges = String(edges.length)
    resize()
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(resize) : null
    observer?.observe(box)

    // 首帧：建场景 + 一次完整绘制（收敛从下一帧开始，账目分开记）。
    const t0 = performance.now()
    syncPositions()
    highlight(selectedRef.current, matches ?? null)
    frameCamera()
    render()
    perf.first = performance.now() - t0
    publish()

    let raf = 0
    let frames = 0
    const loop = () => {
      raf = 0
      const t = performance.now()
      if (sim.running) {
        sim.tick()
        syncPositions()
        // 2D 布局收敛期间图会从原点铺开几千个世界单位：相机必须跟着重取景，
        // 否则一旦收敛就整片飞出视锥（黑屏）。每 15 帧一次，落定后再收一次。
        // 用户动过相机之后不再取景——否则放大贴近的镜头每 15 帧被拽回全局。
        frames += 1
        if (!userMoved && frames % 15 === 0) frameCamera()
      }
      render()
      const cost = performance.now() - t
      perf.frames.push(cost)
      if (perf.frames.length > 180) perf.frames.shift()
      const now = performance.now()
      if (now - lastPublish > 500) {
        lastPublish = now
        publish()
      }
      if (sim.running) raf = requestAnimationFrame(loop)
      else if (!userMoved) {
        frameCamera()
        render()
        publish()
      }
    }
    if (sim.running) raf = requestAnimationFrame(loop)

    // 拖动/滚轮的绘制合并到 rAF：OrbitControls 每次指针移动都发 change，
    // 同步画一遍在真实数据上是 17–25ms/次移动；合并后每帧最多画一次。
    let pendingDraw = 0
    const drawOnce = () => {
      pendingDraw = 0
      const t = performance.now()
      render()
      perf.frames.push(performance.now() - t)
      if (perf.frames.length > 180) perf.frames.shift()
      publish()
    }
    const onControlChange = () => {
      if (pendingDraw) return
      pendingDraw = requestAnimationFrame(drawOnce)
    }
    const onControlStart = () => {
      userMoved = true
    }
    controls.addEventListener('change', onControlChange)
    controls.addEventListener('start', onControlStart)

    // 点选：按下到抬起的位移小于阈值才算点击，转完镜头松手不会误开面板。
    // 命中判据用屏幕空间最近点：星点在世界里只有几像素宽、距离又在几千单位量级，
    // 正文用世界坐标阈值的射线（26/100/500 全空、2000 才误中别的节点）不可靠。
    let downX = 0
    let downY = 0
    let downAt = 0
    const onPointerDown = (event: PointerEvent) => {
      downX = event.clientX
      downY = event.clientY
      downAt = performance.now()
    }
    const onPointerUp = (event: PointerEvent) => {
      if (Math.hypot(event.clientX - downX, event.clientY - downY) > 5) return
      if (performance.now() - downAt > 700) return
      const rect = canvas.getBoundingClientRect()
      const px = event.clientX - rect.left
      const py = event.clientY - rect.top
      const projected = new THREE.Vector3()
      let best = -1
      let bestDist = Infinity
      for (let i = 0; i < nodes.length; i += 1) {
        const node = nodes[i]
        projected.set(node.x, node.y, node.z).project(camera)
        if (projected.z < -1 || projected.z > 1) continue
        const sx = ((projected.x + 1) / 2) * rect.width
        const sy = ((1 - projected.y) / 2) * rect.height
        const dist = Math.hypot(sx - px, sy - py)
        if (dist < bestDist) {
          bestDist = dist
          best = i
        }
      }
      // 星点画成 7px，命中半径取两倍，手指/触摸也够。
      if (best >= 0 && bestDist <= 14) {
        const node = nodes[best]
        if (node) pickRef.current(node.id)
      }
    }
    const onDoubleClick = () => {
      // 双击回正：恢复自动取景的唯一入口。
      userMoved = false
      frameCamera()
      render()
    }
    const onContextLost = (event: Event) => {
      event.preventDefault()
      setContextLost(true)
    }
    const onContextRestored = () => {
      setContextLost(false)
      resize()
      render()
    }
    canvas.addEventListener('webglcontextlost', onContextLost)
    canvas.addEventListener('webglcontextrestored', onContextRestored)
    canvas.addEventListener('pointerdown', onPointerDown)
    canvas.addEventListener('pointerup', onPointerUp)
    canvas.addEventListener('dblclick', onDoubleClick)

    const focusOn = (id: string) => {
      const i = index.get(id)
      if (i === undefined) return
      const node = nodes[i]
      // 这是用户主动定位，之后的收敛不得把镜头拽回全局。
      userMoved = true
      const offset = camera.position.clone().sub(controls.target)
      controls.target.set(node.x, node.y, node.z)
      camera.position.copy(controls.target).add(offset)
      controls.update()
      render()
    }
    focusRef.current = focusOn

    // 只读钩子：算法 用它比对「渲染用的坐标 == layout3d 输出」，漂移必须为 0。
    ;(window as unknown as { __twGraph3DPositions?: () => unknown }).__twGraph3DPositions = () =>
      nodes.map((n) => ({ id: n.id, x: n.x, y: n.y, z: n.z }))

    // 只读钩子：把节点投到画布像素，报四边留白（%）。拟合边距是几何量，
    // 用截图数亮像素会被面板、图例、浮层干扰，量不准。
    ;(window as unknown as { __twGraph3DProject?: () => unknown }).__twGraph3DProject = () => {
      const rect = box.getBoundingClientRect()
      const width = Math.max(rect.width, 1)
      const height = Math.max(rect.height, 1)
      const v = new THREE.Vector3()
      let minX = Infinity
      let minY = Infinity
      let maxX = -Infinity
      let maxY = -Infinity
      for (const n of nodes) {
        v.set(n.x, n.y, n.z).project(camera)
        if (v.z >= 1) continue
        const px = ((v.x + 1) / 2) * width
        const py = ((1 - v.y) / 2) * height
        if (px < minX) minX = px
        if (px > maxX) maxX = px
        if (py < minY) minY = py
        if (py > maxY) maxY = py
      }
      const pct = (v2: number, total: number) => Math.round((v2 / total) * 1000) / 10
      return {
        width: Math.round(width),
        height: Math.round(height),
        top: pct(minY, height),
        bottom: pct(height - maxY, height),
        left: pct(minX, width),
        right: pct(width - maxX, width),
      }
    }

    return () => {
      focusRef.current = null
      if (raf) cancelAnimationFrame(raf)
      if (pendingDraw) cancelAnimationFrame(pendingDraw)
      observer?.disconnect()
      controls.removeEventListener('change', onControlChange)
      controls.removeEventListener('start', onControlStart)
      canvas.removeEventListener('webglcontextlost', onContextLost)
      canvas.removeEventListener('webglcontextrestored', onContextRestored)
      canvas.removeEventListener('pointerdown', onPointerDown)
      canvas.removeEventListener('pointerup', onPointerUp)
      canvas.removeEventListener('dblclick', onDoubleClick)
      if ((window as unknown as { __twGraph3DPositions?: unknown }).__twGraph3DPositions) {
        delete (window as unknown as { __twGraph3DPositions?: unknown }).__twGraph3DPositions
      }
      pointGeometry.dispose()
      pointMaterial.dispose()
      lineGeometry.dispose()
      lineMaterial.dispose()
      texture?.dispose()
      controls.dispose()
      renderer.dispose()
      renderer.forceContextLoss()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [model])

  useEffect(() => {
    if (selected) focusRef.current?.(selected)
  }, [selected])

  return (
    <div className="tw-graph-3d absolute inset-0 bg-canvas" data-testid="graph-3d-view">
      <div ref={boxRef} className="h-full w-full" />
      <canvas
        ref={canvasRef}
        data-testid="graph-3d"
        aria-label="三维星图：拖动旋转、滚轮缩放、点节点开面板、双击回正视图"
        role="application"
        className="absolute inset-0 block h-full w-full cursor-grab touch-none active:cursor-grabbing"
      />
      <div className="pointer-events-none absolute bottom-2 right-3 max-w-[280px] rounded-lg border border-hairline bg-card/85 px-2.5 py-1.5 text-[11px] leading-relaxed text-ink-3 shadow-soft backdrop-blur">
        3D 星图：上层是项目与终点，中层是方向，下层是事实。拖动旋转 · 滚轮缩放 · 双击回正
        <br />
        全部边都在 3D 里；找一条链回链路树
      </div>
    </div>
  )
}

export default GraphStarMap3D

