"use client";

import { useQuery } from "@tanstack/react-query";
import { chatSessionTaskRunsOptions } from "@multica/core/chat/queries";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import type { AgentTask } from "@multica/core/types/agent";
import { Coins } from "@multica/ui/icons";
import { Popover, PopoverContent, PopoverTrigger } from "@multica/ui/components/ui/popover";
import { useFormatSpend } from "../../issues/components/task-run-labels";
import { formatTokens, hasTokenFigure, summarizeTaskUsageAcross } from "../../runtimes/utils";
import { useT } from "../../i18n";
import { formatElapsedMs } from "../lib/format";

/** Time spent running, over the runs that both started and ended. */
function totalRunMs(runs: readonly AgentTask[]): number {
  let total = 0;
  for (const run of runs) {
    if (!run.started_at || !run.completed_at) continue;
    const ms = Date.parse(run.completed_at) - Date.parse(run.started_at);
    if (ms > 0) total += ms;
  }
  return total;
}

function UsageRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[5.5rem_minmax(0,1fr)] items-start gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 text-foreground tabular-nums">{value}</span>
    </div>
  );
}

/**
 * What a chat has used so far: tokens and estimated cost in the header, with
 * the split one click away. It reads the same per-run usage an issue's
 * execution log does, so the two surfaces price a run identically.
 *
 * Renders nothing until some run of the chat has reported usage — an empty
 * figure would claim the chat was free — and nothing once the server stops
 * answering for this chat.
 */
export function ChatSessionUsage({ sessionId }: { sessionId: string }) {
  const { t } = useT("chat");
  const { t: tAgents } = useT("agents");
  const formatSpend = useFormatSpend();
  const { data: runs = [], isError } = useQuery(chatSessionTaskRunsOptions(sessionId));
  // summarizeTaskUsageAcross prices through this store imperatively;
  // subscribing is what makes a saved rate change reach the figure.
  useCustomPricingStore((s) => s.pricings);

  const usage = summarizeTaskUsageAcross(runs.map((run) => run.usage));
  // A failed refresh keeps the last result in the cache, but the server may be
  // refusing because the viewer can no longer read this chat (its agent was
  // made private, say). What it would not send again is not shown again.
  if (!usage || isError) return null;
  const runMs = totalRunMs(runs);

  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type="button"
            aria-label={t(($) => $.usage.label)}
            className="flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5 text-micro tabular-nums outline-none transition-colors hover:bg-accent/50 focus-visible:ring-1 focus-visible:ring-ring"
          />
        }
      >
        <Coins aria-hidden="true" className="h-3 w-3 text-muted-foreground" />
        {hasTokenFigure(usage) && (
          <>
            <span className="font-medium">{formatTokens(usage.tokens)}</span>
            <span className="text-faint-foreground">·</span>
          </>
        )}
        <span className="text-muted-foreground">{formatSpend(usage)}</span>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-64 max-w-[calc(100vw-2rem)] p-3">
        <div className="mb-2 text-caption font-medium text-foreground">{t(($) => $.usage.label)}</div>
        <div className="space-y-1 text-caption">
          <UsageRow label={t(($) => $.usage.runs)} value={String(runs.length)} />
          {runMs > 0 && <UsageRow label={t(($) => $.usage.run_time)} value={formatElapsedMs(runMs)} />}
          <div className="my-2 h-px bg-border" />
          <UsageRow label={tAgents(($) => $.transcript.details_input)} value={formatTokens(usage.input)} />
          <UsageRow label={tAgents(($) => $.transcript.details_output)} value={formatTokens(usage.output)} />
          {usage.cacheRead > 0 && (
            <UsageRow label={tAgents(($) => $.transcript.details_cache_read)} value={formatTokens(usage.cacheRead)} />
          )}
          {usage.cacheWrite > 0 && (
            <UsageRow label={tAgents(($) => $.transcript.details_cache_write)} value={formatTokens(usage.cacheWrite)} />
          )}
          <UsageRow label={tAgents(($) => $.transcript.details_cost)} value={formatSpend(usage)} />
        </div>
      </PopoverContent>
    </Popover>
  );
}
