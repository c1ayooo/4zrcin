// 渗透链路：点一个节点时，画布高亮与侧栏跳表共用的那一条链。
//
// 骨架仍是 model.ts 的 `mainPath`（最深上溯 + 最长下溯）——链的判断规则只在那
// 一处，这里不另造一条。本文件补的是 `mainPath` 没有的两样：
//
//   1. 每一跳是怎么接上的：premise（事实 → 意图，这一步需要的前提）还是
//      intent（意图 → 事实，这一步的产出），附上连接它的边 id，画布据此加粗；
//   2. 主干之外的一跳旁支：同一个事实还被哪些意图消费、这些意图又产出了什么。
//      渗透链路是一张 DAG，砍成单线就丢了「同一个 fact 还能喂给别的意图」。
//
// 侧栏跳表按主干编号，旁支只计数并可跳过去；图上主干编号、邻域次亮、其余压暗。
import {
  mainPath,
  type EdgeKind,
  type GraphEdge,
  type GraphModel,
  type GraphNode,
} from './model.ts'

/** 一件事从哪来：前提（事实 → 意图）还是产出（意图 → 事实）。 */
export type HopRelation = 'premise' | 'produce'

export interface ChainHop {
  /** 1 起，与画布上画在节点上的编号一致。 */
  index: number
  id: string
  type: GraphNode['type']
  title: string
  /** 事实的 kind。 */
  kind?: string
  /** 意图状态 / 事实状态。 */
  status?: string
  /** 入账时间（orderTime），侧栏用来读先后。 */
  at?: string
  /** 第一跳没有关系：主干的起点。 */
  relation: HopRelation | null
  /** 与上一跳之间的边（通常一条）。 */
  edges: string[]
  /** 事实跳：主干之外还在消费这个事实的意图。 */
  otherUses: { id: string; title: string }[]
}

export interface Chain {
  /** 主干跳表，顺序即编号。 */
  hops: ChainHop[]
  /** 主干节点 id，顺序同 hops（画布 focus）。 */
  main: string[]
  /** 一跳旁支的节点（画布次亮）。 */
  near: Set<string>
  /** 主干上每一跳之间的边（画布加粗）。 */
  mainEdges: Set<string>
  /** 旁支相关的边（画布次亮）。 */
  nearEdges: Set<string>
}

/** 只有这两种边让工作往前走，与 model.ts 的 CHAIN_KINDS 同一口径。 */
const CHAIN_KINDS: ReadonlySet<EdgeKind> = new Set<EdgeKind>(['premise', 'intent'])

/** 一条事实被哪些意图当成前提读过（同一事实可喂给多个意图）。 */
export function premiseConsumers(model: Pick<GraphModel, 'edges' | 'byId'>, factID: string): string[] {
  const out: string[] = []
  for (const e of model.edges) {
    if (e.kind !== 'premise' || e.source !== factID) continue
    const target = model.byId.get(e.target)
    if (!target || (target.type !== 'step' && target.type !== 'intent')) continue
    if (!out.includes(e.target)) out.push(e.target)
  }
  return out
}

/** 一跳的关系说法，侧栏与图例用同一些字。 */
export function hopRelationLabel(relation: HopRelation | null): string {
  switch (relation) {
    case 'premise':
      return '前提'
    case 'produce':
      return '产出'
    default:
      return '起点'
  }
}

export function chainView(model: GraphModel, id: string | null): Chain | null {
  const path = mainPath(model, id)
  if (!path || path.length === 0) return null
  const main = new Set(path)

  // 一次遍历建两张索引：无序点对 → 边（跳与跳之间），节点 → 边（旁支）。
  const byPair = new Map<string, GraphEdge[]>()
  const incident = new Map<string, GraphEdge[]>()
  const pairKey = (a: string, b: string) => (a < b ? `${a}|${b}` : `${b}|${a}`)
  for (const e of model.edges) {
    if (!CHAIN_KINDS.has(e.kind)) continue
    if (!model.byId.has(e.source) || !model.byId.has(e.target)) continue
    const key = pairKey(e.source, e.target)
    const bucket = byPair.get(key)
    if (bucket) bucket.push(e)
    else byPair.set(key, [e])
    for (const end of [e.source, e.target]) {
      const rows = incident.get(end)
      if (rows) rows.push(e)
      else incident.set(end, [e])
    }
  }

  // 主干每一跳之间的边与关系。
  const hopEdges: string[][] = path.map(() => [])
  const hopRelations: (HopRelation | null)[] = path.map(() => null)
  const mainEdges = new Set<string>()
  for (let i = 1; i < path.length; i++) {
    const a = path[i - 1]
    const b = path[i]
    const between = byPair.get(pairKey(a, b)) ?? []
    if (between.length === 0) continue
    hopEdges[i] = between.map((e) => e.id)
    hopRelations[i] = between[0].kind === 'premise' ? 'premise' : 'produce'
    for (const e of between) mainEdges.add(e.id)
  }

  // 一跳旁支：主干节点的链上邻居，再加旁支意图自己产出的事实。
  const near = new Set<string>()
  const addNear = (candidate: string) => {
    if (main.has(candidate) || !model.byId.has(candidate)) return
    near.add(candidate)
  }
  for (const nodeID of path) {
    for (const e of incident.get(nodeID) ?? []) addNear(e.source === nodeID ? e.target : e.source)
  }
  for (const nodeID of [...near]) {
    const node = model.byId.get(nodeID)
    if (!node || (node.type !== 'step' && node.type !== 'intent')) continue
    for (const e of incident.get(nodeID) ?? []) addNear(e.source === nodeID ? e.target : e.source)
  }

  const nearEdges = new Set<string>()
  for (const [key, rows] of byPair) {
    const [a, b] = key.split('|')
    if (mainEdges.size > 0 && rows.every((e) => mainEdges.has(e.id))) continue
    if (!main.has(a) && !main.has(b) && !near.has(a) && !near.has(b)) continue
    for (const e of rows) if (!mainEdges.has(e.id)) nearEdges.add(e.id)
  }

  const hops: ChainHop[] = path.map((nodeID, i) => {
    const node = model.byId.get(nodeID)
    const others =
      node?.type === 'fact'
        ? premiseConsumers(model, nodeID)
            .filter((intentID) => !main.has(intentID))
            .map((intentID) => ({ id: intentID, title: model.byId.get(intentID)?.label ?? intentID }))
        : []
    return {
      index: i + 1,
      id: nodeID,
      type: node?.type ?? 'fact',
      title: node?.label ?? nodeID,
      kind: node?.kind,
      status: node?.status,
      at: node?.orderTime,
      relation: hopRelations[i],
      edges: hopEdges[i],
      otherUses: others,
    }
  })

  return {
    hops,
    main: path,
    near,
    mainEdges,
    nearEdges,
  }
}

/** 链上的上游/下游邻居（只沿 premise / intent，与 mainPath 同一口径）。 */
function chainNeighbours(model: Pick<GraphModel, 'nodes' | 'edges' | 'byId'>): {
  up: Map<string, string[]>
  down: Map<string, string[]>
} {
  const up = new Map<string, string[]>()
  const down = new Map<string, string[]>()
  const push = (map: Map<string, string[]>, key: string, value: string) => {
    const rows = map.get(key)
    if (rows) {
      if (!rows.includes(value)) rows.push(value)
    } else map.set(key, [value])
  }
  for (const e of model.edges) {
    if (!CHAIN_KINDS.has(e.kind)) continue
    if (!model.byId.has(e.source) || !model.byId.has(e.target)) continue
    push(down, e.source, e.target)
    push(up, e.target, e.source)
  }
  return { up, down }
}

/**
 * 「只看这棵树」：点中节点的**全部祖先**（不只是主干那一条）+ 它自己 + 向下
 * `down` 跳的产物。
 *
 * 链路是一张 DAG：一步可以有多个前提，一个事实也可以喂给多个意图，所以再看
 * 「从起点怎么走到这里」时要的是所有分支，而不是 mainPath 那根线——这就是它
 * 与 `chainView` 的分工（那个是编号主干，这个是整棵树）。
 *
 * 不设上限：集合多大就多大，调用方在面板上如实报节点数。
 */
export function subtree(model: GraphModel, id: string | null, down = 2): Set<string> | null {
  if (!id) return null
  // 空环（w:…）代表它的意图：点环等价于点那一步。
  const ghostStep = id.startsWith('w:') ? `s:${id.slice(2)}` : null
  const seed = ghostStep && model.byId.has(ghostStep) ? ghostStep : model.byId.has(id) ? id : null
  if (!seed) return null
  const { up, down: successors } = chainNeighbours(model)
  const out = new Set<string>([seed])
  // 祖先：沿前提/产出边一路向上，同一步的每个前提、每条分支都走。
  const queue = [...(up.get(seed) ?? [])]
  const seen = new Set<string>([seed])
  while (queue.length > 0) {
    const cur = queue.shift()!
    if (seen.has(cur) || !model.byId.has(cur)) continue
    seen.add(cur)
    out.add(cur)
    for (const parent of up.get(cur) ?? []) if (!seen.has(parent)) queue.push(parent)
  }
  // 下游：只带 `down` 跳产物，再往下就不是「这棵树」而是整条下游了。
  let frontier = [seed]
  for (let depth = 0; depth < down; depth++) {
    const next: string[] = []
    for (const cur of frontier) {
      for (const child of successors.get(cur) ?? []) {
        if (out.has(child) || !model.byId.has(child)) continue
        out.add(child)
        next.push(child)
      }
    }
    frontier = next
    if (frontier.length === 0) break
  }
  // 意图的产物被这棵树切掉时，图里会把它画成空环（w:…）。环不是另一跳，是同一
  // 个意图的另一种画法；把它算进集合，「这棵树 N 个节点」才等于屏幕上真画出来的
  // 节点数，过滤后的节点数也才与集合大小一致。
  for (const id of [...out]) {
    if (!id.startsWith('s:')) continue
    const produced = (successors.get(id) ?? []).filter((child) => out.has(child))
    if (produced.length === 0) out.add(`w:${id.slice(2)}`)
  }
  return out
}
