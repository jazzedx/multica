import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "./queries";

export const issueDeliveryKeys = {
  all: (wsId: string) => [...issueKeys.all(wsId), "delivery-overview"] as const,
  /**
   * `revision` is whatever the caller already holds that changes when the
   * work moves on (the sub-issues' statuses, say), so the overview follows
   * without an event of its own.
   */
  issue: (wsId: string, issueId: string, revision: string) =>
    [...issueDeliveryKeys.all(wsId), issueId, revision] as const,
};

export function issueDeliveryOverviewOptions(wsId: string, issueId: string, revision = "") {
  return queryOptions({
    queryKey: issueDeliveryKeys.issue(wsId, issueId, revision),
    queryFn: () => api.getIssueDeliveryOverview(issueId),
    refetchOnMount: "always",
    // The previous figures stay up while a new revision loads.
    placeholderData: (previous) => previous,
  });
}

/** A lead time in the largest two units that matter, for a caller to word. */
export type LeadTime =
  | { unit: "under_minute" }
  | { unit: "minutes"; minutes: number }
  | { unit: "hours"; hours: number; minutes: number }
  | { unit: "days"; days: number; hours: number };

export function leadTime(startedAt: string, finishedAt: string | null, now: number): LeadTime | null {
  const start = Date.parse(startedAt);
  const end = finishedAt ? Date.parse(finishedAt) : now;
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) return null;
  const minutes = Math.floor((end - start) / 60_000);
  if (minutes < 1) return { unit: "under_minute" };
  if (minutes < 60) return { unit: "minutes", minutes };
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return { unit: "hours", hours, minutes: minutes % 60 };
  return { unit: "days", days: Math.floor(hours / 24), hours: hours % 24 };
}
