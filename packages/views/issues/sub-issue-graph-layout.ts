// Layered layout for a parent's dependency graph: every issue sits one column
// right of the last issue it waits for, so the picture reads left to right in
// the order the work can happen. The graphs are a handful of sub-issues, so
// this is the plain longest-path layering with a barycenter pass, not a
// general graph library.

export interface GraphNodeInput {
  id: string;
  /** Stable tie-breaker within a column, e.g. the issue number. */
  order: number;
}

export interface GraphEdgeInput {
  /** The issue waited for. */
  from: string;
  /** The issue that waits. */
  to: string;
}

export interface GraphSize {
  nodeWidth: number;
  nodeHeight: number;
  gapX: number;
  gapY: number;
}

export interface PlacedNode {
  id: string;
  layer: number;
  row: number;
  x: number;
  y: number;
}

export interface PlacedEdge extends GraphEdgeInput {
  /** SVG path from the right edge of `from` to the left edge of `to`. */
  path: string;
  /** Where the edge arrives, for an end marker. */
  end: { x: number; y: number };
}

export interface GraphLayout {
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  width: number;
  height: number;
}

/** Sweeps of the barycenter pass. Two settle graphs of this size. */
const ORDERING_SWEEPS = 2;

export function layoutDependencyGraph(
  nodes: GraphNodeInput[],
  edges: GraphEdgeInput[],
  size: GraphSize,
): GraphLayout {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const seen = new Set<string>();
  const kept = edges.filter((edge) => {
    const key = `${edge.from}>${edge.to}`;
    if (edge.from === edge.to || !byId.has(edge.from) || !byId.has(edge.to) || seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });

  const incoming = new Map<string, string[]>(nodes.map((node) => [node.id, []]));
  const outgoing = new Map<string, string[]>(nodes.map((node) => [node.id, []]));
  for (const edge of kept) {
    incoming.get(edge.to)!.push(edge.from);
    outgoing.get(edge.from)!.push(edge.to);
  }

  // Longest path from a source, in topological order. The server refuses
  // cycles; if one arrives anyway its members keep column 0 rather than
  // looping here.
  const layerOf = new Map<string, number>(nodes.map((node) => [node.id, 0]));
  const pending = new Map<string, number>(nodes.map((node) => [node.id, incoming.get(node.id)!.length]));
  const queue = nodes.filter((node) => pending.get(node.id) === 0).map((node) => node.id);
  while (queue.length > 0) {
    const id = queue.shift()!;
    for (const next of outgoing.get(id)!) {
      layerOf.set(next, Math.max(layerOf.get(next)!, layerOf.get(id)! + 1));
      pending.set(next, pending.get(next)! - 1);
      if (pending.get(next) === 0) queue.push(next);
    }
  }

  const layerCount = Math.max(0, ...layerOf.values()) + (nodes.length > 0 ? 1 : 0);
  const layers: string[][] = Array.from({ length: layerCount }, () => []);
  for (const node of [...nodes].sort((a, b) => a.order - b.order)) {
    layers[layerOf.get(node.id)!]!.push(node.id);
  }

  // An edge that skips columns would run straight through whatever sits in
  // between and read as an edge to it. Give it a slot of its own in every
  // column it crosses, ordered and placed like a node.
  const orderOf = new Map(nodes.map((node) => [node.id, node.order]));
  const lastOrder = Math.max(0, ...orderOf.values());
  const viaOf = new Map<GraphEdgeInput, string[]>();
  kept.forEach((edge, index) => {
    const first = layerOf.get(edge.from)! + 1;
    const last = layerOf.get(edge.to)!;
    if (last <= first) return;
    const via: string[] = [];
    for (let layer = first; layer < last; layer += 1) {
      const id = `\u0000via:${index}:${layer}`;
      via.push(id);
      layers[layer]!.push(id);
      // After the real nodes of its column until the pass below moves it.
      orderOf.set(id, lastOrder + 1 + index);
      incoming.set(id, []);
      outgoing.set(id, []);
    }
    const chain = [edge.from, ...via, edge.to];
    incoming.set(edge.to, incoming.get(edge.to)!.filter((id) => id !== edge.from));
    outgoing.set(edge.from, outgoing.get(edge.from)!.filter((id) => id !== edge.to));
    for (let i = 1; i < chain.length; i += 1) {
      incoming.get(chain[i]!)!.push(chain[i - 1]!);
      outgoing.get(chain[i - 1]!)!.push(chain[i]!);
    }
    viaOf.set(edge, via);
  });

  // Order each column by where its neighbours sit, so edges cross less.
  const rowOf = new Map<string, number>();
  const assignRows = () => layers.forEach((layer) => layer.forEach((id, row) => rowOf.set(id, row)));
  assignRows();
  const reorder = (layer: string[], neighbours: Map<string, string[]>) => {
    const weight = (id: string) => {
      const rows = neighbours.get(id)!.map((other) => rowOf.get(other)!);
      return rows.length === 0 ? null : rows.reduce((sum, row) => sum + row, 0) / rows.length;
    };
    const weights = new Map(layer.map((id) => [id, weight(id)]));
    layer.sort((a, b) => {
      const wa = weights.get(a) ?? rowOf.get(a)!;
      const wb = weights.get(b) ?? rowOf.get(b)!;
      return wa - wb || orderOf.get(a)! - orderOf.get(b)!;
    });
    layer.forEach((id, row) => rowOf.set(id, row));
  };
  for (let sweep = 0; sweep < ORDERING_SWEEPS; sweep += 1) {
    for (let i = 1; i < layers.length; i += 1) reorder(layers[i]!, incoming);
    for (let i = layers.length - 2; i >= 0; i -= 1) reorder(layers[i]!, outgoing);
  }

  const stepX = size.nodeWidth + size.gapX;
  const stepY = size.nodeHeight + size.gapY;
  const tallest = Math.max(0, ...layers.map((layer) => layer.length));
  const placed = new Map<string, PlacedNode>();
  layers.forEach((layer, layerIndex) => {
    // Shorter columns are centred against the tallest one.
    const offset = ((tallest - layer.length) * stepY) / 2;
    layer.forEach((id, row) => {
      placed.set(id, { id, layer: layerIndex, row, x: layerIndex * stepX, y: offset + row * stepY });
    });
  });

  const bend = size.gapX / 2;
  const middle = (id: string) => placed.get(id)!.y + size.nodeHeight / 2;
  const placedEdges = kept.map((edge): PlacedEdge => {
    let x = placed.get(edge.from)!.x + size.nodeWidth;
    let y = middle(edge.from);
    let path = `M ${x} ${y}`;
    // Curve across each gap, and run level through a crossed column's slot.
    const curveTo = (tx: number, ty: number) => {
      path += ` C ${x + bend} ${y}, ${tx - bend} ${ty}, ${tx} ${ty}`;
      x = tx;
      y = ty;
    };
    for (const id of viaOf.get(edge) ?? []) {
      curveTo(placed.get(id)!.x, middle(id));
      x += size.nodeWidth;
      path += ` L ${x} ${y}`;
    }
    curveTo(placed.get(edge.to)!.x, middle(edge.to));
    return { ...edge, path, end: { x, y } };
  });

  return {
    nodes: nodes.map((node) => placed.get(node.id)!),
    edges: placedEdges,
    width: layerCount === 0 ? 0 : layerCount * stepX - size.gapX,
    height: tallest === 0 ? 0 : tallest * stepY - size.gapY,
  };
}
