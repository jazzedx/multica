// @vitest-environment node
import { describe, expect, it } from "vitest";
import { layoutDependencyGraph, type GraphEdgeInput } from "./sub-issue-graph-layout";

const SIZE = { nodeWidth: 100, nodeHeight: 40, gapX: 20, gapY: 10 };

const nodes = (...ids: string[]) => ids.map((id, order) => ({ id, order }));
const edge = (from: string, to: string): GraphEdgeInput => ({ from, to });

function columns(ids: string[], edges: GraphEdgeInput[]) {
  const layout = layoutDependencyGraph(nodes(...ids), edges, SIZE);
  const out: string[][] = [];
  for (const node of [...layout.nodes].sort((a, b) => a.row - b.row)) {
    (out[node.layer] ??= []).push(node.id);
  }
  return { layout, columns: out };
}

describe("layoutDependencyGraph", () => {
  it("puts each issue one column after the last issue it waits for", () => {
    // design -> api, ui -> test, and test also waits directly for design.
    const { columns: cols } = columns(
      ["design", "api", "ui", "test"],
      [edge("design", "api"), edge("design", "ui"), edge("api", "test"), edge("ui", "test"), edge("design", "test")],
    );
    expect(cols).toEqual([["design"], ["api", "ui"], ["test"]]);
  });

  it("centres a shorter column against the tallest one", () => {
    const { layout } = columns(["a", "b", "c", "d"], [edge("a", "b"), edge("a", "c"), edge("b", "d"), edge("c", "d")]);
    const y = Object.fromEntries(layout.nodes.map((node) => [node.id, node.y]));
    expect(y.b).toBe(0);
    expect(y.c).toBe(50);
    // One node beside two: half a step down.
    expect(y.a).toBe(25);
    expect(y.d).toBe(25);
    expect(layout.width).toBe(3 * 100 + 2 * 20);
    expect(layout.height).toBe(2 * 40 + 10);
  });

  it("keeps issues without dependencies in the first column", () => {
    const { columns: cols } = columns(["a", "b", "alone"], [edge("a", "b")]);
    expect(cols).toEqual([["a", "alone"], ["b"]]);
  });

  it("orders a column by where its inputs sit, so edges do not cross", () => {
    // a1 feeds b2 and a2 feeds b1: number order would cross the two edges.
    const { columns: cols } = columns(["a1", "a2", "b1", "b2"], [edge("a1", "b2"), edge("a2", "b1")]);
    expect(cols).toEqual([["a1", "a2"], ["b2", "b1"]]);
  });

  it("draws each edge from the right of its source to the left of its target", () => {
    const { layout } = columns(["a", "b"], [edge("a", "b")]);
    expect(layout.edges).toEqual([
      { from: "a", to: "b", path: "M 100 20 C 110 20, 110 20, 120 20", end: { x: 120, y: 20 } },
    ]);
  });

  // Drawn straight, a -> c would pass through b and read as an edge to it.
  it("routes an edge that skips a column through a slot of its own", () => {
    const { layout, columns: cols } = columns(["a", "b", "c"], [edge("a", "b"), edge("b", "c"), edge("a", "c")]);
    expect(cols).toEqual([["a"], ["b"], ["c"]]);
    const y = Object.fromEntries(layout.nodes.map((node) => [node.id, node.y]));
    // The middle column holds b and the slot, so it is two rows tall.
    expect(layout.height).toBe(2 * 40 + 10);
    expect(y.b).toBe(0);
    const skipping = layout.edges.find((e) => e.from === "a" && e.to === "c")!;
    // Level across the middle column (x 120 to 220) in the row under b.
    expect(skipping.path).toContain("120 70 L 220 70");
    expect(skipping.end).toEqual({ x: 240, y: y.c! + 20 });
  });

  it("ignores edges it cannot draw: unknown ends, repeats and self-loops", () => {
    const { layout, columns: cols } = columns(
      ["a", "b"],
      [edge("a", "b"), edge("a", "b"), edge("a", "a"), edge("ghost", "b"), edge("a", "ghost")],
    );
    expect(cols).toEqual([["a"], ["b"]]);
    expect(layout.edges).toHaveLength(1);
  });

  it("still places every node when the edges form a cycle", () => {
    const { layout } = columns(["a", "b", "c"], [edge("a", "b"), edge("b", "a"), edge("b", "c")]);
    expect(layout.nodes.map((node) => node.id).sort()).toEqual(["a", "b", "c"]);
    expect(layout.nodes.every((node) => Number.isFinite(node.x) && Number.isFinite(node.y))).toBe(true);
  });

  it("is empty for no nodes", () => {
    expect(layoutDependencyGraph([], [], SIZE)).toEqual({ nodes: [], edges: [], width: 0, height: 0 });
  });
});
