// The asset graph's model builder (spec #1 工单7/#8). The server sends the
// hierarchy; this module turns it into the GraphModel the existing canvas,
// toolbar, minimap and list view already draw — no second renderer.
//
// Node vocabulary: the asset kinds (domain / range / host / service / url /
// vuln / evidence / intent) ride the fact node style; the drill-down root is a
// project-shaped pill, the one node the layered layout needs as its anchor.
// Fence standing, when present, is the fill (nodeColor in graph/render.ts), so
// "inside / waiting / refused / no record" reads before the taxonomy.

import type { GraphEdge, GraphModel, GraphNode } from '@/graph/model'
import type { AssetGraph } from './types.ts'

export const ASSET_FACT_KINDS: ReadonlySet<string> = new Set([
  'domain',
  'range',
  'host',
  'service',
  'url',
  'vuln',
  'evidence',
  'intent',
])

/** The label the list view and the node panel show for one asset kind. */
export function assetKindLabel(kind: string | undefined): string {
  switch (kind) {
    case 'domain':
      return '域名'
    case 'range':
      return '网段'
    case 'host':
      return '主机'
    case 'service':
      return '端口'
    case 'url':
      return 'URL'
    case 'vuln':
      return '漏洞'
    case 'evidence':
      return '证据'
    case 'intent':
      return '意图'
    default:
      return '资产'
  }
}

export function assetRowNodeID(key: string): string {
  return `asset:row:${key}`
}

/** Normalize the fence standing for drawing: an asset row with no stored
 *  standing is 未登记, never "outside the fence" (the rule table is not here). */
export function assetFenceOf(node: { fence?: string; kind?: string }): string | undefined {
  if (node.fence) return node.fence
  if (node.kind === 'domain' || node.kind === 'range' || node.kind === 'host') return 'unregistered'
  return undefined
}

/** Deterministic opening positions, so a reload does not reshuffle the map.
 *  The simulation/layered layout own the final picture; these only seed it. */
function ringPosition(id: string, index: number, total: number): { x: number; y: number } {
  let h = 2166136261
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  const angle = (index / Math.max(total, 1)) * Math.PI * 2
  const ring = 120 + Math.sqrt(index + 1) * 66
  return {
    x: Math.cos(angle) * ring + (((h >>> 0) / 4294967295) - 0.5) * 70,
    y: Math.sin(angle) * ring + ((((h >>> 8) >>> 0) / 4294967295) - 0.5) * 70,
  }
}

/** Build the canvas model. `data` may be absent while the first read is in
 *  flight; the empty model keeps the canvas mounted without inventing nodes. */
export function buildAssetGraphModel(data: AssetGraph | undefined): GraphModel {
  const incoming = new Map<string, string[]>()
  const outgoing = new Map<string, string[]>()
  const byId = new Map<string, GraphNode>()
  const edgesById = new Map<string, GraphEdge>()
  if (!data) {
    return emptyModel(byId, edgesById, incoming, outgoing)
  }
  const rootID = data.full ? '' : assetRowNodeID(data.key ?? '')
  const nodes: GraphNode[] = data.nodes.map((n, index) => {
    const type: GraphNode['type'] = n.id === rootID ? 'project' : 'fact'
    const position = ringPosition(n.id, index, data.nodes.length)
    return {
      id: n.id,
      type,
      label: n.label,
      sub: n.sub,
      kind: n.kind,
      fence: assetFenceOf(n),
      status: n.kind === 'intent' ? n.status : undefined,
      x: position.x,
      y: position.y,
      vx: 0,
      vy: 0,
      degree: 0,
      w: 0,
      h: 0,
      depth: 0,
      orderTime: `${String(index).padStart(6, '0')}`,
    }
  })
  for (const node of nodes) byId.set(node.id, node)

  const edges: GraphEdge[] = []
  for (const e of data.edges) {
    if (!byId.has(e.source) || !byId.has(e.target)) continue
    // Both relations are structural, so both draw as the grey containment
    // line and the layered layout can place them as tree edges. The relation's
    // own name lives in the node panel, not on the canvas.
    const edge: GraphEdge = { id: e.id, source: e.source, target: e.target, kind: 'brief' }
    edges.push(edge)
    edgesById.set(edge.id, edge)
    const out = outgoing.get(e.source) ?? []
    out.push(e.target)
    outgoing.set(e.source, out)
    const inc = incoming.get(e.target) ?? []
    inc.push(e.source)
    incoming.set(e.target, inc)
  }
  for (const node of nodes) {
    node.degree = (incoming.get(node.id)?.length ?? 0) + (outgoing.get(node.id)?.length ?? 0)
  }
  // Depth: hops from the drill-down root (or the row itself in the full view,
  // where every row is its own root at depth 0).
  const roots = nodes.filter((n) => (incoming.get(n.id)?.length ?? 0) === 0)
  let maxDepth = 0
  const queue = roots.map((n) => n.id)
  const seen = new Set(queue)
  for (const node of roots) node.depth = 0
  while (queue.length > 0) {
    const id = queue.shift() as string
    const node = byId.get(id)
    const depth = node?.depth ?? 0
    if (depth > maxDepth) maxDepth = depth
    for (const next of outgoing.get(id) ?? []) {
      if (seen.has(next)) continue
      seen.add(next)
      const child = byId.get(next)
      if (child) child.depth = depth + 1
      queue.push(next)
    }
  }
  return { nodes, edges, byId, maxDepth, edgesById, stepByIntent: new Map(), factsById: new Map(), incoming, outgoing }
}

function emptyModel(
  byId: Map<string, GraphNode>,
  edgesById: Map<string, GraphEdge>,
  incoming: Map<string, string[]>,
  outgoing: Map<string, string[]>,
): GraphModel {
  return {
    nodes: [],
    edges: [],
    byId,
    maxDepth: 0,
    edgesById,
    stepByIntent: new Map(),
    factsById: new Map(),
    incoming,
    outgoing,
  }
}
