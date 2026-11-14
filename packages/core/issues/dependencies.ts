import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { IssueDependencies } from "../types";
import { issueKeys } from "./queries";

/**
 * Issue dependencies: an issue that waits for others starts no run until they
 * are all done, then starts on its own.
 *
 * The keys sit under the workspace's issue keys, so whatever drops all issue
 * data drops these too.
 */
export const issueDependencyKeys = {
  all: (wsId: string) => [...issueKeys.all(wsId), "dependencies"] as const,
  issue: (wsId: string, issueId: string) =>
    [...issueDependencyKeys.all(wsId), "issue", issueId] as const,
  subIssues: (wsId: string, parentId: string) =>
    [...issueDependencyKeys.all(wsId), "sub-issues", parentId] as const,
};

/**
 * How often a view that shows something waiting looks again. No realtime event
 * says "the issue you wait for is done" to the waiting issue's views, and a
 * release is the moment a reader is watching for.
 */
const WAITING_REFETCH_MS = 10_000;

/** True while at least one of the issues waited for is not done. */
export function isWaiting(dependencies: Pick<IssueDependencies, "blocked_by"> | undefined): boolean {
  return (dependencies?.blocked_by ?? []).some((blocker) => !blocker.done);
}

export function issueDependenciesOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: issueDependencyKeys.issue(wsId, issueId),
    queryFn: () => api.listIssueDependencies(issueId),
    refetchOnMount: "always",
    refetchInterval: (query) => (isWaiting(query.state.data) ? WAITING_REFETCH_MS : false),
  });
}

/** Every dependency of a parent's sub-issues, in one read for the whole list. */
export function subIssueBlockersOptions(wsId: string, parentId: string) {
  return queryOptions({
    queryKey: issueDependencyKeys.subIssues(wsId, parentId),
    queryFn: () => api.listSubIssueBlockers(parentId),
    refetchOnMount: "always",
    refetchInterval: (query) =>
      (query.state.data?.edges ?? []).some((edge) => !edge.blocked_by.done) ? WAITING_REFETCH_MS : false,
  });
}

/**
 * Stops an issue waiting for one of its blockers. The server starts the issue
 * when that was the last thing holding it.
 */
export function useRemoveIssueDependency(wsId: string, issueId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (blockerId: string) => api.removeIssueDependency(issueId, blockerId),
    onSuccess: (dependencies) => {
      // The response is this issue's new state; everything else that shows
      // the edge (its other end, a sub-issue list) has to ask again.
      const own = issueDependencyKeys.issue(wsId, issueId);
      qc.setQueryData(own, dependencies);
      qc.invalidateQueries({
        queryKey: issueDependencyKeys.all(wsId),
        predicate: (query) => query.queryKey.join("/") !== own.join("/"),
      });
    },
  });
}
