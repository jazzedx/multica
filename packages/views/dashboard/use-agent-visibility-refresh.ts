"use client";

import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { dashboardKeys } from "@multica/core/dashboard";

/**
 * Refetch the dashboard when the set of agents the viewer can name changes.
 *
 * The server decides per request which agents a per-agent rollup may name and
 * folds the rest into one bucket. The agent list follows an access change at
 * once, over the realtime channel, but the rollups only poll: until they did,
 * an agent the viewer just lost access to still arrived by id, resolved to no
 * name, and was shown as deleted — and one just shared with them stayed hidden
 * in the bucket. Asking again as soon as the list changes closes that gap.
 *
 * `agentIds` is undefined while the list loads; the first loaded list is the
 * baseline, not a change.
 */
export function useAgentVisibilityRefresh(wsId: string, agentIds: readonly string[] | undefined) {
  const queryClient = useQueryClient();
  const signature = agentIds ? [...agentIds].sort().join(",") : undefined;
  const seen = useRef<{ wsId: string; signature: string } | null>(null);

  useEffect(() => {
    if (signature === undefined) return;
    const previous = seen.current;
    seen.current = { wsId, signature };
    // Another workspace's list is a different dashboard, not a change to this one.
    if (!previous || previous.wsId !== wsId || previous.signature === signature) return;
    void queryClient.invalidateQueries({ queryKey: dashboardKeys.all(wsId) });
  }, [queryClient, wsId, signature]);
}
