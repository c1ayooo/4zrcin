// 3D 星图的布局：**不跑三维力导向**。
//
// 上一次否决的正是三维力导向：563 个点会被斥力推到一层球壳上，从外面看就是星云。
// 这一次的第三轴来自结构——项目/目标在最上层，方向居中，事实和边界在下层，层内
// 再按度数微抬一点；同层用确定性的哈希微扰错开，避免完全重叠。x/y 直接沿用 2D
// 力导向的坐标，所以三维版读起来仍是这张图，只是被「立起来」了。
import type { GraphModel, GraphNode } from '@/graph/model'

export interface Point3 {
  x: number
  y: number
  z: number
}

/** 层与层之间的世界高度。 */
export const LAYER_HEIGHT = 420
/** 同层内的确定性微扰幅度（世界单位）。 */
export const LAYER_JITTER = 34

/** 类型 → 层号：越大越靠上。 */
export function layerOf(node: Pick<GraphNode, 'type' | 'status'>): number {
  switch (node.type) {
    case 'project':
      return 1
    case 'goal':
      return node.status === 'met' ? 0.86 : 0.78
    case 'intent':
    case 'step':
      return 0
    case 'ghost':
      return -0.02
    case 'boundary':
      return -0.92
    case 'fact':
    default:
      return -0.7
  }
}

/** 确定性哈希：同一个 id 每次落到同一个位置，截图与几何度量才可复现。 */
export function hash01(id: string, salt: number): number {
  let h = 2166136261 ^ Math.imul(salt + 1, 0x9e3779b1)
  for (let i = 0; i < id.length; i += 1) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  h ^= h >>> 13
  h = Math.imul(h, 0x5bd1e995)
  h ^= h >>> 15
  return (h >>> 0) / 4294967295
}

/** 度数带来的层内抬升：枢纽节点略高，读得出「这一层里谁更重」。 */
function degreeLift(degree: number | undefined): number {
  const d = Math.max(0, Math.min(degree ?? 0, 24))
  return (d / 24) * 0.12
}

/**
 * 层高随图的 xy 跨度自适应：固定 420 在真实数据上只有 xy 的 0.08，三维会变成
 * 侧看的刀片。取 xy 分位跨度的 0.5，夹在 420–6000，保证层与层在屏幕上分得开。
 */
export function layerHeightFor(nodes: Array<Pick<GraphNode, 'x' | 'y'>>): number {
  if (nodes.length === 0) return LAYER_HEIGHT
  const q = (pick: (n: { x: number; y: number }) => number) => {
    const sorted = nodes.map(pick).sort((a, b) => a - b)
    const at = (p: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))]
    return at(0.98) - at(0.02)
  }
  const span = Math.hypot(q((n) => n.x), q((n) => n.y))
  return Math.min(6000, Math.max(LAYER_HEIGHT, span * 0.5))
}

export function layeredZAt(node: GraphNode, height: number): number {
  const jitter = (hash01(node.id, 1) - 0.5) * 2 * (LAYER_JITTER * (height / LAYER_HEIGHT))
  return height * (layerOf(node) + degreeLift(node.degree)) + jitter
}

export function layeredZ(node: GraphNode): number {
  const jitter = (hash01(node.id, 1) - 0.5) * 2 * LAYER_JITTER
  return LAYER_HEIGHT * (layerOf(node) + degreeLift(node.degree)) + jitter
}

/** 一个节点的三维坐标：x/y 来自 2D 布局，z 来自类型分层。 */
export function layeredPosition(node: GraphNode): Point3 {
  return { x: node.x, y: node.y, z: layeredZ(node) }
}

/**
 * 一次算完整张图的 z（测试与离线尺子用）。返回 id → z，不复制节点。
 */
export function layerZs(model: Pick<GraphModel, 'nodes'>): Map<string, number> {
  const out = new Map<string, number>()
  for (const node of model.nodes) out.set(node.id, layeredZ(node))
  return out
}
/**
 * 一次调用算出与 `model.nodes` 同序的三维坐标（算法 的尺子按 A 契约读这个）。
 * 没有 tick、没有迭代：z 是结构分层的纯函数结果。
 */
export interface Layout3DItem extends Point3 {
  id: string
}

export function layout3d(model: Pick<GraphModel, 'nodes'>): Layout3DItem[] {
  return model.nodes.map((node) => ({ id: node.id, x: node.x, y: node.y, z: layeredZ(node) }))
}