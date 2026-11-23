// 绘制层聚合：力导向图里 798 条边几乎全是从意图节点扇出到事实的同一类边，
// 全部画出来就是一条汇进一个点的河。聚合把每个意图的事实用一条折线串起来，
// 最后收到意图节点——观感从「河」变成 84 条星座线，而数据、物理、点选都不变。
//
// 纪律：这里只产出「绘制用的边」，不参与物理、命中测试、面板与搜索。
// 物理仍用原始边（否则布局会变），命中仍用原始边（否则点线判定会漂）。
import type { EdgeKind, GraphEdge, GraphNode } from './model.ts'

/** 折线沿用原来的边类型与元数据，仅改 id 与端点。 */
export interface ChainEdge extends GraphEdge {
  /** 同一条折线的序号，0 表示从意图节点出发的第一段。 */
  chainIndex: number
}

export interface AggregateResult {
  edges: ChainEdge[]
  /** 有事实但没聚合进折线的意图（正常应为 0）。 */
  uncovered: string[]
  /** 实际画出的折线条数（每个意图一条，含只有 1 个事实的意图）。 */
  chains: number
  /** 被折线吸收的原始边数。 */
  absorbed: number
}

/**
 * 把一个意图扇出到事实的边收成一条折线：意图 → 事实₁ → 事实₂ → … 。
 * 事实按围绕意图节点的极角排序，折线才不会自己打结；同角度按 id 稳定排序。
 * 其余类型的边原样保留。
 */
export function aggregateFan(edges: readonly GraphEdge[], nodes: readonly GraphNode[]): AggregateResult {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const fans = new Map<string, GraphEdge[]>()
  const rest: ChainEdge[] = []
  for (const edge of edges) {
    const fan =
      edge.kind === 'intent' && byId.has(edge.source) && byId.has(edge.target) && edge.target.startsWith('f:')
    if (!fan) {
      rest.push({ ...edge, chainIndex: -1 })
      continue
    }
    const list = fans.get(edge.source) ?? []
    list.push(edge)
    fans.set(edge.source, list)
  }

  const edgesOut: ChainEdge[] = []
  const uncovered: string[] = []
  let absorbed = 0
  let chains = 0
  for (const [source, list] of fans) {
    const anchor = byId.get(source)
    if (!anchor || list.length === 0) {
      uncovered.push(source)
      continue
    }
    const angle = (edge: GraphEdge) => {
      const at = byId.get(edge.target)
      return at ? Math.atan2(at.y - anchor.y, at.x - anchor.x) : 0
    }
    const ordered = [...list].sort((a, b) => {
      const delta = angle(a) - angle(b)
      return Math.abs(delta) > 1e-9 ? delta : a.target.localeCompare(b.target)
    })
    // 只有一个事实的意图也画一段：它是「这个意图产出了什么」的一根线，不是孤立点。
    let prev = source
    ordered.forEach((edge, i) => {
      edgesOut.push({
        ...edge,
        id: `chain:${source}:${i}`,
        source: prev,
        target: edge.target,
        chainIndex: i,
      })
      prev = edge.target
      absorbed += 1
    })
    chains += 1
  }
  return { edges: [...rest, ...edgesOut], uncovered, chains, absorbed }
}

/** 只给力导向用：链路树是读链视图，箭头与逐边关系都要保留。 */
export function aggregateForDraw(
  edges: readonly GraphEdge[],
  nodes: readonly GraphNode[],
  layered: boolean,
): AggregateResult | null {
  if (layered) return null
  return aggregateFan(edges, nodes)
}

/** 边类型在折线里不改色：折线仍是「指派 → 产出」这一条关系。 */
export const CHAIN_EDGE_KIND: EdgeKind = 'intent'
