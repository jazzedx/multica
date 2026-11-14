import type { DashboardUsageBySource } from "@multica/core/types";
import { estimateCost } from "../runtimes/utils";
import { DELETED_AGENTS_ROW_ID, RESTRICTED_AGENTS_ROW_ID } from "./utils";

/** One agent's part of a source. */
export interface SourceAgentUsage {
  /** An agent id, or one of the two synthetic bucket ids the leaderboard uses. */
  agentId: string;
  tokens: number;
  cost: number;
  taskCount: number;
  /** 0–1 of its source, on the same measure as the source's own share. */
  share: number;
}

/** One source's share of the workspace's usage, and the agents behind it. */
export interface SourceUsageRow {
  source: string;
  tokens: number;
  cost: number;
  taskCount: number;
  /** 0–1 of the total: of cost, or of tokens when nothing in the window has a cost. */
  share: number;
  /** Largest first. */
  agents: SourceAgentUsage[];
}

interface Tally {
  tokens: number;
  cost: number;
  taskCount: number;
}

function add(tally: Tally, row: DashboardUsageBySource) {
  tally.tokens +=
    row.input_tokens + row.output_tokens + row.cache_read_tokens + row.cache_write_tokens;
  tally.cost += estimateCost(row);
  tally.taskCount += row.task_count;
}

/**
 * Fold the per-(source, agent, provider, model) rows into one row per source,
 * each with its agents, largest first.
 *
 * Cost is summed per row because each row is priced at its own model's rate,
 * the same reason the wire format keeps the model dimension. Shares are of
 * cost — the figure people budget by — unless no row can be priced, where they
 * fall back to tokens so the breakdown still says something.
 *
 * An agent the caller cannot name (`knownAgentIds`) and the server did not
 * already fold away has been deleted; those land on one bucket per source, as
 * they do on the leaderboard.
 */
export function aggregateUsageBySource(
  rows: readonly DashboardUsageBySource[],
  knownAgentIds: ReadonlySet<string>,
): SourceUsageRow[] {
  const bySource = new Map<string, Tally & { agents: Map<string, Tally> }>();
  for (const row of rows) {
    let source = bySource.get(row.source);
    if (!source) {
      source = { tokens: 0, cost: 0, taskCount: 0, agents: new Map() };
      bySource.set(row.source, source);
    }
    add(source, row);

    const agentId =
      row.agent_id === RESTRICTED_AGENTS_ROW_ID || knownAgentIds.has(row.agent_id)
        ? row.agent_id
        : DELETED_AGENTS_ROW_ID;
    let agent = source.agents.get(agentId);
    if (!agent) {
      agent = { tokens: 0, cost: 0, taskCount: 0 };
      source.agents.set(agentId, agent);
    }
    add(agent, row);
  }

  let totalCost = 0;
  let totalTokens = 0;
  for (const source of bySource.values()) {
    totalCost += source.cost;
    totalTokens += source.tokens;
  }
  // One measure for the whole breakdown, so a source's agents are shares of
  // the same thing the source is a share of.
  const byCost = totalCost > 0;
  const measure = (tally: Tally) => (byCost ? tally.cost : tally.tokens);
  const total = byCost ? totalCost : totalTokens;
  const ratio = (part: number, whole: number) => (whole > 0 ? part / whole : 0);

  return [...bySource.entries()]
    .map(([name, source]): SourceUsageRow => ({
      source: name,
      tokens: source.tokens,
      cost: source.cost,
      taskCount: source.taskCount,
      share: ratio(measure(source), total),
      agents: [...source.agents.entries()]
        .map(([agentId, agent]): SourceAgentUsage => ({
          agentId,
          ...agent,
          share: ratio(measure(agent), measure(source)),
        }))
        .sort((a, b) => b.share - a.share || b.tokens - a.tokens || a.agentId.localeCompare(b.agentId)),
    }))
    .sort((a, b) => b.share - a.share || b.tokens - a.tokens || a.source.localeCompare(b.source));
}
