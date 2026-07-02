import { useEffect, useRef } from 'react'
import { Application, Container, Graphics, Text, type FederatedPointerEvent } from 'pixi.js'
import {
  forceSimulation,
  forceLink,
  forceManyBody,
  forceCenter,
  forceCollide,
  type Simulation,
} from 'd3-force'
import type { Graph, GraphNode } from '../api'

// This is the same stack Obsidian's graph view uses: a WebGL renderer (PIXI.js)
// driven by a force-directed simulation (d3-force). Nodes repel, links act as
// springs, dragging a node perturbs its neighbors, and releasing lets the graph
// spring back and settle.

export interface FNode {
  id: string
  kind: 'source' | 'memory'
  label: string
  metric: number
  scope: string
  // d3-force-owned, mutated in place across renders.
  x?: number
  y?: number
  vx?: number
  vy?: number
  fx?: number | null
  fy?: number | null
}

export interface FLink {
  id: string
  source: string | FNode
  target: string | FNode
  rel: 'link' | 'relation'
}

export interface GraphData {
  nodes: FNode[]
  links: FLink[]
}

const MEMORY = 0x7c8cf0
const SOURCE = 0x3aa8a0
const RELATION = 0x6f76c9
const EDGE = 0x3a3a44
const LABEL_MEMORY = 0xc7ccf5
const LABEL_SOURCE = 0x93b0b2

function radius(node: FNode): number {
  return node.kind === 'memory'
    ? 5 + 1.1 * Math.min(node.metric, 12)
    : 4 + 0.7 * Math.min(node.metric, 12)
}

function shortLabel(node: FNode): string {
  const max = node.kind === 'memory' ? 34 : 22
  return node.label.length > max ? node.label.slice(0, max) + '…' : node.label
}

function linkEnd(v: string | FNode): string {
  return typeof v === 'object' ? v.id : v
}

// toGraphData maps a graph read-model into the force-graph shape. Link edges
// (Source→Memory) and Relation edges (Memory↔Memory) are tagged so the two edge
// families render distinctly; edges referencing a capped-out node are dropped.
export function toGraphData(graph: Graph): GraphData {
  const ids = new Set(graph.nodes.map((n) => n.id))
  const nodes: FNode[] = graph.nodes.map((n: GraphNode) => ({
    id: n.id,
    kind: n.kind,
    label: n.label,
    metric: n.size,
    scope: n.scope?.value ?? '',
  }))
  const links: FLink[] = []
  for (const e of graph.edges) {
    if (!ids.has(e.source_id) || !ids.has(e.memory_id)) continue
    links.push({ id: `e:${e.source_id}:${e.memory_id}`, source: e.source_id, target: e.memory_id, rel: 'link' })
  }
  for (const r of graph.relations) {
    if (!ids.has(r.from_memory_id) || !ids.has(r.to_memory_id)) continue
    links.push({ id: `r:${r.from_memory_id}:${r.to_memory_id}`, source: r.from_memory_id, target: r.to_memory_id, rel: 'relation' })
  }
  return { nodes, links }
}

interface Selection {
  id: string
  kind: string
  label: string
  metric: number
  scope: string
}

// Engine holds the imperative PIXI + d3-force state for the component's life.
interface Engine {
  destroy: () => void
  sync: (data: GraphData) => void
}

interface Props {
  data: GraphData
  onSelect: (node: Selection) => void
  onExpand: (memoryId: string) => void
}

export function GraphCanvas({ data, onSelect, onExpand }: Props) {
  const hostRef = useRef<HTMLDivElement>(null)
  const engineRef = useRef<Engine | null>(null)
  const cbRef = useRef({ onSelect, onExpand })
  cbRef.current = { onSelect, onExpand }
  const pendingData = useRef(data)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let cancelled = false
    const app = new Application()

    app
      .init({
        resizeTo: host,
        antialias: true,
        backgroundAlpha: 0,
        resolution: window.devicePixelRatio || 1,
        autoDensity: true,
      })
      .then(() => {
        if (cancelled) {
          app.destroy(true)
          return
        }
        host.appendChild(app.canvas)
        engineRef.current = createEngine(app, cbRef)
        engineRef.current.sync(pendingData.current)
      })

    return () => {
      cancelled = true
      engineRef.current?.destroy()
      engineRef.current = null
      // If init hasn't resolved yet the .then above bails via `cancelled`.
      if (app.renderer) app.destroy(true)
    }
  }, [])

  useEffect(() => {
    pendingData.current = data
    engineRef.current?.sync(data)
  }, [data])

  return (
    <div
      ref={hostRef}
      className="min-w-0 flex-1 touch-none"
      style={{
        background:
          'radial-gradient(ellipse at 50% -10%, rgba(94,106,210,0.12), transparent 55%), var(--background)',
      }}
    />
  )
}

function createEngine(
  app: Application,
  cbRef: React.RefObject<{ onSelect: (n: Selection) => void; onExpand: (id: string) => void }>,
): Engine {
  const world = new Container()
  const linksG = new Graphics()
  const nodeLayer = new Container()
  world.addChild(linksG)
  world.addChild(nodeLayer)
  app.stage.addChild(world)

  const nodeGfx = new Map<string, Graphics>()
  const labelGfx = new Map<string, Text>()
  let nodes: FNode[] = []
  let links: FLink[] = []
  let adjacency = new Map<string, Set<string>>()
  let hovered: string | null = null
  // Fit the whole graph into view once it has mostly settled after a (re)load.
  let fitPending = false

  const sim: Simulation<FNode, FLink> = forceSimulation<FNode>([])
    .force('charge', forceManyBody<FNode>().strength(-150))
    .force('link', forceLink<FNode, FLink>([]).id((d) => d.id).distance(48).strength(0.6))
    .force('center', forceCenter(0, 0))
    .force('collide', forceCollide<FNode>((d) => radius(d) + 3))
    .alphaDecay(0.02)
    .velocityDecay(0.35)
    .on('tick', draw)

  // Center the world in the viewport; the force center is (0,0).
  function recenter() {
    world.position.set(app.screen.width / 2, app.screen.height / 2)
  }
  recenter()

  const lit = (id: string) =>
    !hovered || id === hovered || (adjacency.get(hovered)?.has(id) ?? false)

  function fitView() {
    if (nodes.length === 0) return
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const n of nodes) {
      const r = radius(n)
      minX = Math.min(minX, (n.x ?? 0) - r)
      maxX = Math.max(maxX, (n.x ?? 0) + r)
      minY = Math.min(minY, (n.y ?? 0) - r)
      maxY = Math.max(maxY, (n.y ?? 0) + r)
    }
    const cw = maxX - minX || 1
    const ch = maxY - minY || 1
    const scale = Math.max(0.15, Math.min(2.2, Math.min(app.screen.width / cw, app.screen.height / ch) * 0.86))
    world.scale.set(scale)
    world.position.set(
      app.screen.width / 2 - ((minX + maxX) / 2) * scale,
      app.screen.height / 2 - ((minY + maxY) / 2) * scale,
    )
  }

  function draw() {
    if (fitPending && sim.alpha() < 0.1) {
      fitView()
      fitPending = false
    }
    const showLabels = world.scale.x > 1.35
    for (const n of nodes) {
      const g = nodeGfx.get(n.id)
      if (!g) continue
      g.position.set(n.x ?? 0, n.y ?? 0)
      g.alpha = lit(n.id) ? 1 : 0.12
      const label = labelGfx.get(n.id)
      if (label) {
        const show = (showLabels || n.id === hovered) && lit(n.id)
        label.visible = show
        if (show) label.position.set(n.x ?? 0, (n.y ?? 0) + radius(n) + 2)
      }
    }
    linksG.clear()
    for (const l of links) {
      const s = l.source as FNode
      const t = l.target as FNode
      if (s.x == null || t.x == null) continue
      const on = lit(s.id) && lit(t.id)
      if (l.rel === 'relation') {
        drawDashed(linksG, s.x, s.y!, t.x, t.y!)
        linksG.stroke({ width: 1.4, color: RELATION, alpha: on ? 0.9 : 0.06 })
      } else {
        linksG.moveTo(s.x, s.y!).lineTo(t.x, t.y!)
        linksG.stroke({ width: 1, color: EDGE, alpha: on ? 0.85 : 0.12 })
        if (on) drawArrow(linksG, s.x, s.y!, t.x, t.y!, radius(t))
      }
    }
  }

  function makeNode(n: FNode) {
    const g = new Graphics()
    const r = radius(n)
    if (n.kind === 'memory') {
      g.circle(0, 0, r).fill(MEMORY)
    } else {
      g.roundRect(-r, -r * 0.75, r * 2, r * 1.5, 3).fill(SOURCE)
    }
    g.eventMode = 'static'
    g.cursor = 'pointer'
    g.on('pointerover', () => {
      hovered = n.id
      draw()
    })
    g.on('pointerout', () => {
      hovered = null
      draw()
    })
    g.on('pointerdown', (e: FederatedPointerEvent) => startNodeDrag(n, e))
    nodeLayer.addChild(g)
    nodeGfx.set(n.id, g)

    const label = new Text({
      text: shortLabel(n),
      style: {
        fontFamily: 'ui-monospace, monospace',
        fontSize: 10,
        fill: n.kind === 'memory' ? LABEL_MEMORY : LABEL_SOURCE,
      },
    })
    label.anchor.set(0.5, 0)
    label.resolution = 2
    label.visible = false
    nodeLayer.addChild(label)
    labelGfx.set(n.id, label)
  }

  function removeNode(id: string) {
    nodeGfx.get(id)?.destroy()
    nodeGfx.delete(id)
    labelGfx.get(id)?.destroy()
    labelGfx.delete(id)
  }

  function sync(data: GraphData) {
    const prevIds = [...nodeGfx.keys()]
    const incoming = new Set(data.nodes.map((n) => n.id))
    // An expand keeps every current node and adds more; a scope change replaces
    // the set. Only re-fit the viewport on a replace, so drilling down doesn't
    // yank the view around.
    const isExpand = prevIds.length > 0 && prevIds.every((id) => incoming.has(id))

    for (const id of prevIds) {
      if (!incoming.has(id)) removeNode(id)
    }
    for (const n of data.nodes) {
      if (!nodeGfx.has(n.id)) makeNode(n)
    }
    nodes = data.nodes
    links = data.links
    adjacency = buildAdjacency(links)

    sim.nodes(nodes)
    ;(sim.force('link') as ReturnType<typeof forceLink<FNode, FLink>>).links(links)
    fitPending = !isExpand
    sim.alpha(isExpand ? 0.5 : 0.8).restart()
  }

  // --- drag (perturbs neighbors) + click ---
  let dragNode: FNode | null = null
  let downPos = { x: 0, y: 0 }
  let moved = false

  function startNodeDrag(n: FNode, e: FederatedPointerEvent) {
    e.stopPropagation()
    dragNode = n
    moved = false
    downPos = { x: e.global.x, y: e.global.y }
    const p = world.toLocal(e.global)
    n.fx = p.x
    n.fy = p.y
    sim.alphaTarget(0.3).restart()
  }

  // --- pan / zoom on the background ---
  let panning = false
  let panStart = { x: 0, y: 0 }
  let worldStart = { x: 0, y: 0 }

  app.stage.eventMode = 'static'
  app.stage.hitArea = app.screen
  app.stage.on('pointerdown', (e: FederatedPointerEvent) => {
    panning = true
    panStart = { x: e.global.x, y: e.global.y }
    worldStart = { x: world.position.x, y: world.position.y }
  })
  app.stage.on('globalpointermove', (e: FederatedPointerEvent) => {
    if (dragNode) {
      if (Math.hypot(e.global.x - downPos.x, e.global.y - downPos.y) > 3) moved = true
      const p = world.toLocal(e.global)
      dragNode.fx = p.x
      dragNode.fy = p.y
    } else if (panning) {
      world.position.set(worldStart.x + (e.global.x - panStart.x), worldStart.y + (e.global.y - panStart.y))
    }
  })
  function endPointer() {
    if (dragNode) {
      const node = dragNode
      dragNode = null
      node.fx = null
      node.fy = null
      sim.alphaTarget(0)
      if (!moved) {
        cbRef.current.onSelect({ id: node.id, kind: node.kind, label: node.label, metric: node.metric, scope: node.scope })
        if (node.kind === 'memory') cbRef.current.onExpand(node.id)
      }
    }
    panning = false
  }
  app.stage.on('pointerup', endPointer)
  app.stage.on('pointerupoutside', endPointer)

  const onWheel = (e: WheelEvent) => {
    e.preventDefault()
    const rect = app.canvas.getBoundingClientRect()
    const px = e.clientX - rect.left
    const py = e.clientY - rect.top
    const before = world.toLocal({ x: px, y: py })
    const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12
    const next = Math.max(0.15, Math.min(5, world.scale.x * factor))
    world.scale.set(next)
    const after = world.toLocal({ x: px, y: py })
    world.position.x += (after.x - before.x) * next
    world.position.y += (after.y - before.y) * next
    draw()
  }
  app.canvas.addEventListener('wheel', onWheel, { passive: false })

  const onResize = () => recenter()
  app.renderer.on('resize', onResize)

  return {
    sync,
    destroy: () => {
      sim.stop()
      app.canvas.removeEventListener('wheel', onWheel)
      app.renderer.off('resize', onResize)
    },
  }
}

function buildAdjacency(links: FLink[]): Map<string, Set<string>> {
  const m = new Map<string, Set<string>>()
  const add = (a: string, b: string) => {
    if (!m.has(a)) m.set(a, new Set())
    m.get(a)!.add(b)
  }
  for (const l of links) {
    add(linkEnd(l.source), linkEnd(l.target))
    add(linkEnd(l.target), linkEnd(l.source))
  }
  return m
}

function drawDashed(g: Graphics, x1: number, y1: number, x2: number, y2: number) {
  const dash = 5
  const gap = 4
  const dx = x2 - x1
  const dy = y2 - y1
  const len = Math.hypot(dx, dy)
  if (len === 0) return
  const ux = dx / len
  const uy = dy / len
  let d = 0
  while (d < len) {
    const s = Math.min(d + dash, len)
    g.moveTo(x1 + ux * d, y1 + uy * d).lineTo(x1 + ux * s, y1 + uy * s)
    d += dash + gap
  }
}

function drawArrow(g: Graphics, x1: number, y1: number, x2: number, y2: number, targetR: number) {
  const dx = x2 - x1
  const dy = y2 - y1
  const len = Math.hypot(dx, dy)
  if (len === 0) return
  const ux = dx / len
  const uy = dy / len
  // Land the arrowhead just outside the target node.
  const tipX = x2 - ux * (targetR + 1)
  const tipY = y2 - uy * (targetR + 1)
  const size = 4
  const ax = -uy
  const ay = ux
  g.moveTo(tipX, tipY)
    .lineTo(tipX - ux * size + ax * size * 0.6, tipY - uy * size + ay * size * 0.6)
    .lineTo(tipX - ux * size - ax * size * 0.6, tipY - uy * size - ay * size * 0.6)
    .lineTo(tipX, tipY)
    .fill({ color: EDGE, alpha: 0.85 })
}
