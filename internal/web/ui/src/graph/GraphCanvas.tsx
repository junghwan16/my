import { useEffect, useRef } from 'react'
import cytoscape, {
  type Core,
  type ElementDefinition,
  type StylesheetStyle,
} from 'cytoscape'
import type { Graph, GraphNode } from '../api'

// Node diameter grows with "melted-in-ness": a Memory by its Relation degree,
// a Source by its fan-out. Both clamp so one huge node never crowds the canvas.
function memoryDiameter(size: number): number {
  return 26 + 8 * Math.min(size, 12)
}
function sourceDiameter(size: number): number {
  return 18 + 4 * Math.min(size, 12)
}

function shortLabel(label: string, kind: string): string {
  const max = kind === 'memory' ? 40 : 24
  return label.length > max ? label.slice(0, max) + '…' : label
}

function nodeElement(node: GraphNode): ElementDefinition {
  const memory = node.kind === 'memory'
  return {
    data: {
      id: node.id,
      kind: node.kind,
      label: node.label,
      shortLabel: shortLabel(node.label, node.kind),
      metric: node.size,
      scope: node.scope?.value ?? '',
      size: memory ? memoryDiameter(node.size) : sourceDiameter(node.size),
    },
  }
}

// toElements maps a graph read-model to cytoscape elements. Link edges
// (Source→Memory) and Relation edges (Memory↔Memory) are tagged so the
// stylesheet renders the two families distinctly; edges referencing a
// capped-out node are dropped.
export function toElements(graph: Graph): ElementDefinition[] {
  const ids = new Set(graph.nodes.map((n) => n.id))
  const elements: ElementDefinition[] = graph.nodes.map(nodeElement)
  for (const edge of graph.edges) {
    if (!ids.has(edge.source_id) || !ids.has(edge.memory_id)) continue
    elements.push({
      data: {
        id: `e:${edge.source_id}:${edge.memory_id}`,
        rel: 'link',
        source: edge.source_id,
        target: edge.memory_id,
      },
    })
  }
  for (const rel of graph.relations) {
    if (!ids.has(rel.from_memory_id) || !ids.has(rel.to_memory_id)) continue
    elements.push({
      data: {
        id: `r:${rel.from_memory_id}:${rel.to_memory_id}`,
        rel: 'relation',
        source: rel.from_memory_id,
        target: rel.to_memory_id,
      },
    })
  }
  return elements
}

const stylesheet: StylesheetStyle[] = [
  {
    selector: "node[kind='memory']",
    style: {
      'background-color': '#7c8cf0',
      label: 'data(shortLabel)',
      color: '#c7ccf5',
      'font-family': 'ui-monospace, monospace',
      'font-size': '9px',
      'text-valign': 'center',
      'text-halign': 'center',
      'text-wrap': 'ellipsis',
      'text-max-width': '64px',
      'text-margin-y': -1,
      width: 'data(size)',
      height: 'data(size)',
    },
  },
  {
    selector: "node[kind='source']",
    style: {
      'background-color': '#3aa8a0',
      shape: 'round-rectangle',
      label: 'data(shortLabel)',
      color: '#8a9ba0',
      'font-family': 'ui-monospace, monospace',
      'font-size': '8px',
      'text-valign': 'bottom',
      'text-margin-y': 3,
      width: 'data(size)',
      height: 'data(size)',
    },
  },
  {
    selector: 'node:selected',
    style: { 'border-width': 2, 'border-color': '#f7f8f8' },
  },
  {
    selector: "edge[rel='link']",
    style: {
      width: 1,
      'line-color': '#35353d',
      'target-arrow-color': '#35353d',
      'target-arrow-shape': 'triangle',
      'curve-style': 'bezier',
      'arrow-scale': 0.7,
    },
  },
  {
    selector: "edge[rel='relation']",
    style: {
      width: 2,
      'line-color': '#6f76c9',
      'line-style': 'dashed',
      'curve-style': 'bezier',
    },
  },
  // Obsidian-style hover emphasis: everything not near the hovered node fades.
  { selector: '.faded', style: { opacity: 0.15 } },
]

interface Props {
  elements: ElementDefinition[]
  onSelect: (node: {
    id: string
    kind: string
    label: string
    metric: number
    scope: string
  }) => void
  onExpand: (memoryId: string) => void
}

// GraphCanvas wraps cytoscape imperatively: one instance for the component's
// life, synced to `elements` by adding new nodes/edges and dropping absent ones
// (a scope switch replaces all; an expand only appends). Layout re-runs when the
// element set changes.
export function GraphCanvas({ elements, onSelect, onExpand }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<Core | null>(null)
  const handlers = useRef({ onSelect, onExpand })
  handlers.current = { onSelect, onExpand }

  useEffect(() => {
    if (!containerRef.current) return
    const cy = cytoscape({
      container: containerRef.current,
      style: stylesheet,
      layout: { name: 'grid' },
      wheelSensitivity: 0.2,
      minZoom: 0.2,
      maxZoom: 3,
    })
    cyRef.current = cy

    cy.on('tap', 'node', (event) => {
      const data = event.target.data()
      handlers.current.onSelect({
        id: data.id,
        kind: data.kind,
        label: data.label,
        metric: data.metric ?? 0,
        scope: data.scope ?? '',
      })
      if (data.kind === 'memory') handlers.current.onExpand(data.id)
    })

    cy.on('mouseover', 'node', (event) => {
      const node = event.target
      const keep = node.closedNeighborhood()
      cy.elements().difference(keep).addClass('faded')
    })
    cy.on('mouseout', 'node', () => cy.elements().removeClass('faded'))

    return () => {
      cy.destroy()
      cyRef.current = null
    }
  }, [])

  useEffect(() => {
    const cy = cyRef.current
    if (!cy) return
    const incoming = new Map(elements.map((el) => [el.data.id as string, el]))
    const present = new Set<string>()
    cy.elements().forEach((el) => {
      const id = el.id()
      if (incoming.has(id)) present.add(id)
      else el.remove()
    })
    const toAdd = elements.filter((el) => !present.has(el.data.id as string))
    if (toAdd.length > 0) {
      cy.add(toAdd)
      cy.layout({ name: 'cose', animate: false, nodeDimensionsIncludeLabels: true }).run()
    }
  }, [elements])

  return (
    <div
      ref={containerRef}
      className="min-w-0 flex-1"
      style={{
        background:
          'radial-gradient(ellipse at 50% -10%, rgba(94,106,210,0.10), transparent 55%), var(--background)',
      }}
    />
  )
}
