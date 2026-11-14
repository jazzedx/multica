// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { DashboardUsageBySource } from "@multica/core/types";
import { aggregateUsageBySource } from "./by-source";
import { DELETED_AGENTS_ROW_ID, RESTRICTED_AGENTS_ROW_ID } from "./utils";

// A model no rate table knows, so a row's cost is exactly what it reports.
function row(
  source: string,
  agentId: string,
  overrides: Partial<DashboardUsageBySource> = {},
): DashboardUsageBySource {
  return {
    source,
    agent_id: agentId,
    provider: "some-provider",
    model: "a-model-with-no-known-rate",
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    task_count: 0,
    ...overrides,
  };
}

const usd = (dollars: number) => dollars * 1e10;
const known = new Set(["ada", "turing"]);

describe("aggregateUsageBySource", () => {
  it("folds a source's models into one row and ranks sources by cost", () => {
    const rows = aggregateUsageBySource(
      [
        row("chat", "ada", { input_tokens: 100, output_tokens: 20, cost_usd_ticks: usd(1), task_count: 2 }),
        row("issue", "ada", { input_tokens: 50, cost_usd_ticks: usd(6), task_count: 1 }),
        row("chat", "ada", {
          model: "another-unknown-model",
          cache_read_tokens: 300,
          cost_usd_ticks: usd(1),
          task_count: 1,
        }),
      ],
      known,
    );

    expect(rows.map((r) => r.source)).toEqual(["issue", "chat"]);
    expect(rows[0]).toMatchObject({ tokens: 50, cost: 6, taskCount: 1, share: 0.75 });
    expect(rows[1]).toMatchObject({ tokens: 420, cost: 2, taskCount: 3, share: 0.25 });
  });

  it("breaks each source down by agent, as shares of that source", () => {
    const [chat, issue] = aggregateUsageBySource(
      [
        row("chat", "turing", { input_tokens: 10, cost_usd_ticks: usd(1), task_count: 1 }),
        row("chat", "ada", { input_tokens: 30, cost_usd_ticks: usd(3), task_count: 2 }),
        row("chat", "ada", { model: "another-unknown-model", input_tokens: 5, cost_usd_ticks: usd(4), task_count: 1 }),
        row("issue", "turing", { input_tokens: 7, cost_usd_ticks: usd(2), task_count: 1 }),
      ],
      known,
    );

    expect(chat?.agents).toEqual([
      { agentId: "ada", tokens: 35, cost: 7, taskCount: 3, share: 0.875 },
      { agentId: "turing", tokens: 10, cost: 1, taskCount: 1, share: 0.125 },
    ]);
    expect(issue?.agents).toEqual([{ agentId: "turing", tokens: 7, cost: 2, taskCount: 1, share: 1 }]);
  });

  it("keeps the server's restricted bucket and buckets agents that no longer exist", () => {
    const [chat] = aggregateUsageBySource(
      [
        row("chat", "ada", { cost_usd_ticks: usd(5) }),
        row("chat", RESTRICTED_AGENTS_ROW_ID, { cost_usd_ticks: usd(3) }),
        row("chat", "deleted-1", { cost_usd_ticks: usd(1) }),
        row("chat", "deleted-2", { cost_usd_ticks: usd(1) }),
      ],
      known,
    );

    expect(chat?.agents.map((a) => [a.agentId, a.cost])).toEqual([
      ["ada", 5],
      [RESTRICTED_AGENTS_ROW_ID, 3],
      [DELETED_AGENTS_ROW_ID, 2],
    ]);
  });

  it("shares by tokens when nothing in the window can be priced", () => {
    const rows = aggregateUsageBySource(
      [
        row("autopilot", "ada", { input_tokens: 100 }),
        row("chat", "ada", { input_tokens: 100 }),
        row("chat", "turing", { input_tokens: 200 }),
      ],
      known,
    );

    expect(rows.map((r) => [r.source, r.share])).toEqual([
      ["chat", 0.75],
      ["autopilot", 0.25],
    ]);
    expect(rows[0]?.agents.map((a) => [a.agentId, a.share])).toEqual([
      ["turing", 2 / 3],
      ["ada", 1 / 3],
    ]);
  });

  it("keeps a source this client does not know", () => {
    expect(
      aggregateUsageBySource([row("some-future-source", "ada", { input_tokens: 5 })], known),
    ).toMatchObject([{ source: "some-future-source", tokens: 5, share: 1 }]);
  });

  it("is empty for an empty window", () => {
    expect(aggregateUsageBySource([], known)).toEqual([]);
  });
});
