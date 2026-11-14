"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, EyeOff, Trash2 } from "@multica/ui/icons";
import { dashboardUsageBySourceOptions } from "@multica/core/dashboard";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { agentListOptions } from "@multica/core/workspace/queries";
import { ActorAvatar } from "../../common/actor-avatar";
import { formatTokens } from "../../runtimes/utils";
import { useT } from "../../i18n";
import { aggregateUsageBySource, type SourceAgentUsage } from "../by-source";
import { DELETED_AGENTS_ROW_ID, RESTRICTED_AGENTS_ROW_ID } from "../utils";

const GRID =
  "grid grid-cols-[minmax(9rem,1.4fr)_minmax(4rem,2fr)_4rem_5rem_5rem_4rem] items-center gap-3";

/** Agents listed under a source before the rest collapse behind "show all". */
const AGENT_LIMIT = 5;

function percent(share: number): string {
  const pct = share * 100;
  return pct >= 1 || pct === 0 ? `${Math.round(pct)}%` : "<1%";
}

function ShareBar({ share, className }: { share: number; className: string }) {
  return (
    <div className="relative h-2 overflow-hidden rounded-full bg-muted">
      <div
        className={`h-full rounded-full transition-[width] duration-300 ease-out ${className}`}
        style={{ width: `${share * 100}%` }}
      />
    </div>
  );
}

/**
 * Where the workspace's usage came from — chats, issues, autopilots — and
 * which agents spent it there.
 *
 * Self-contained on purpose: it owns its queries and renders nothing for an
 * empty window, so the page that hosts it needs one line and no knowledge of
 * the breakdown. Refreshing the dashboard reaches it through the shared
 * dashboard key.
 */
export function UsageBySource({
  days,
  projectId,
  tz,
}: {
  days: number;
  projectId: string | null;
  tz: string;
}) {
  const { t } = useT("usage");
  const wsId = useWorkspaceId();
  const { data } = useQuery(dashboardUsageBySourceOptions(wsId, days, projectId, tz));
  const { data: agents } = useQuery(agentListOptions(wsId));
  // aggregateUsageBySource prices through this store imperatively;
  // subscribing is what makes a saved rate change reach the figures.
  useCustomPricingStore((s) => s.pricings);

  const agentNames = useMemo(
    () => new Map((agents ?? []).map((agent) => [agent.id, agent.name])),
    [agents],
  );
  const rows = aggregateUsageBySource(data ?? [], new Set(agentNames.keys()));

  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [showingAll, setShowingAll] = useState<ReadonlySet<string>>(new Set());
  const toggle = (set: ReadonlySet<string>, source: string) => {
    const next = new Set(set);
    if (!next.delete(source)) next.add(source);
    return next;
  };

  // Until the agent list has loaded, every agent would read as deleted.
  if (rows.length === 0 || !agents) return null;

  const sourceLabels: Record<string, string> = {
    chat: t(($) => $.by_source.sources.chat),
    issue: t(($) => $.by_source.sources.issue),
    autopilot: t(($) => $.by_source.sources.autopilot),
    other: t(($) => $.by_source.sources.other),
  };

  const agentCell = (agent: SourceAgentUsage) => {
    if (agent.agentId === DELETED_AGENTS_ROW_ID || agent.agentId === RESTRICTED_AGENTS_ROW_ID) {
      const deleted = agent.agentId === DELETED_AGENTS_ROW_ID;
      const Icon = deleted ? Trash2 : EyeOff;
      return (
        <>
          <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <Icon className="h-3 w-3" />
          </span>
          <span className="min-w-0 truncate text-caption italic text-muted-foreground">
            {deleted ? t(($) => $.leaderboard.deleted_agents) : t(($) => $.leaderboard.other_agents)}
          </span>
        </>
      );
    }
    return (
      <>
        <ActorAvatar actorType="agent" actorId={agent.agentId} size="sm" enableHoverCard />
        <span className="min-w-0 truncate text-caption">{agentNames.get(agent.agentId)}</span>
      </>
    );
  };

  return (
    <section aria-label={t(($) => $.by_source.title)} className="rounded-lg border bg-card">
      <div className="border-b px-4 pt-4 pb-3">
        <h4 className="text-body font-semibold">{t(($) => $.by_source.title)}</h4>
      </div>
      <div className="overflow-x-auto">
        <div className="min-w-[40rem]">
          <div className={`${GRID} border-b px-4 py-2 text-caption font-medium text-muted-foreground`}>
            <span>{t(($) => $.by_source.header_source)}</span>
            <span />
            <span className="text-right">{t(($) => $.by_source.header_share)}</span>
            <span className="text-right">{t(($) => $.leaderboard.header_tokens)}</span>
            <span className="text-right">{t(($) => $.leaderboard.header_cost)}</span>
            <span className="text-right">{t(($) => $.leaderboard.header_tasks)}</span>
          </div>
          <ul className="divide-y">
            {rows.map((row) => {
              // A source from a newer server is shown as sent.
              const label = sourceLabels[row.source] ?? row.source;
              const open = !collapsed.has(row.source);
              const all = showingAll.has(row.source);
              const visibleAgents = all ? row.agents : row.agents.slice(0, AGENT_LIMIT);
              const Chevron = open ? ChevronDown : ChevronRight;
              return (
                <li key={row.source}>
                  <div className={`${GRID} px-4 py-2`}>
                    <button
                      type="button"
                      aria-expanded={open}
                      onClick={() => setCollapsed((current) => toggle(current, row.source))}
                      className="flex min-w-0 items-center gap-1.5 rounded-sm text-left outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    >
                      <Chevron aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 truncate text-body font-medium">{label}</span>
                      <span className="shrink-0 text-caption text-muted-foreground">
                        {t(($) => $.leaderboard.caption, { count: row.agents.length })}
                      </span>
                    </button>
                    <ShareBar share={row.share} className="bg-chart-1" />
                    <span className="text-right text-caption tabular-nums text-muted-foreground">
                      {percent(row.share)}
                    </span>
                    <span className="text-right text-caption tabular-nums text-muted-foreground">
                      {formatTokens(row.tokens)}
                    </span>
                    <span className="text-right text-body font-medium tabular-nums">
                      ${row.cost.toFixed(2)}
                    </span>
                    <span className="text-right text-caption tabular-nums text-muted-foreground">
                      {row.taskCount}
                    </span>
                  </div>
                  {open ? (
                    <>
                      {/* The share here is of the source above, not of the workspace. */}
                      <ul aria-label={label} className="pb-1">
                        {visibleAgents.map((agent) => (
                          <li key={agent.agentId} className={`${GRID} px-4 py-1.5`}>
                            <div className="flex min-w-0 items-center gap-2 pl-5">{agentCell(agent)}</div>
                            <ShareBar share={agent.share} className="bg-chart-1/50" />
                            <span className="text-right text-caption tabular-nums text-muted-foreground">
                              {percent(agent.share)}
                            </span>
                            <span className="text-right text-caption tabular-nums text-muted-foreground">
                              {formatTokens(agent.tokens)}
                            </span>
                            <span className="text-right text-caption tabular-nums">
                              ${agent.cost.toFixed(2)}
                            </span>
                            <span className="text-right text-caption tabular-nums text-muted-foreground">
                              {agent.taskCount}
                            </span>
                          </li>
                        ))}
                      </ul>
                      {row.agents.length > AGENT_LIMIT ? (
                        <button
                          type="button"
                          onClick={() => setShowingAll((current) => toggle(current, row.source))}
                          className="mb-2 ml-9 text-caption text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                        >
                          {all
                            ? t(($) => $.leaderboard.show_less, { count: AGENT_LIMIT })
                            : t(($) => $.leaderboard.show_all)}
                        </button>
                      ) : null}
                    </>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </div>
      </div>
    </section>
  );
}
