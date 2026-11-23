// 3D 星图的对外契约（算法 的尺子按这个形状对打）：
//   new ForceSim(model, { mode }) · sim.tick() · sim.running · sim.nodes[].{id,x,y,z}
//
// 这里的「模拟」不是三维力导向：x/y 由 2D 力导向铺开，z 由 graph3d/layers 按类型
// 分层。tick() 只推进 2D 布局，z 每次同步重算；settled 之后 running 为 false。
import type { GraphModel } from '@/graph/model'
import { ForceSim as FlatForceSim } from '@/graph/sim'
import { layerHeightFor, layeredZAt } from './layers'

export interface Sim3DNode {
  id: string
  x: number
  y: number
  z: number
  /** 半径提示（可选；尺子缺省按 7 处理）。 */
  r: number
}

// 按 id 记住已收敛的 x/y：模型重建时 buildGraph 给的是螺旋初值，直接拿来铺 3D
// 会退回「最近邻 31、62 对重叠」那一列。这里只存坐标，不搬模型、不碰渲染。
const settledXY = new Map<string, { x: number; y: number }>()

export class ForceSim {
  nodes: Sim3DNode[]
  private flat: FlatForceSim
  private model: GraphModel
  private height: number

  constructor(model: GraphModel, _options: { mode?: string } = {}) {
    // 复制节点：2D 画布与这里共用同一份 model 时，谁也不该被对方的 tick 移动。
    this.model = { ...model, nodes: model.nodes.map((node) => ({ ...node })) }
    this.flat = new FlatForceSim(this.model, { mode: 'force' })
    this.height = layerHeightFor(this.model.nodes)
    this.nodes = this.model.nodes.map((node) => ({
      id: node.id,
      x: node.x,
      y: node.y,
      z: layeredZAt(node, this.height),
      r: Math.max(node.w, node.h),
    }))
  }

  get running(): boolean {
    return this.flat.running
  }

  /** 一步 2D 布局；返回是否还在收敛。 */
  tick(): boolean {
    if (this.flat.running) this.flat.tick()
    for (let i = 0; i < this.nodes.length; i += 1) {
      const node = this.model.nodes[i]
      const out = this.nodes[i]
      out.x = node.x
      out.y = node.y
      out.z = layeredZAt(node, this.height)
    }
    return this.running
  }
}
