import { keepPreviousData, queryOptions, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import {
  DashboardHistoryLimitTightenedError,
  isDashboardHistoryLimitTightened,
  isStricterHistoryLimit,
  reportDashboardHistoryLimit,
} from "./history-limit";
import { dashboardKeys, type DashboardHistoryLimitParam } from "./queries";

// Kept apart from queries.ts so the breakdown stays one self-contained
// addition. It follows the conventions documented there: the key shape with
// the history limit the query is made under, the rollup-cadence refresh, and
// carrying the previous result across a range change but never across a
// workspace, project, timezone or history-limit change.
export const dashboardUsageBySourceKey = (
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
  historyLimit: DashboardHistoryLimitParam = null,
) => [...dashboardKeys.all(wsId), "by-source", days, projectId, tz, historyLimit ?? null] as const;

const DAYS_INDEX = 3;

export function dashboardUsageBySourceOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
  historyLimit: DashboardHistoryLimitParam,
) {
  const queryKey = dashboardUsageBySourceKey(wsId, days, projectId, tz, historyLimit);
  return queryOptions({
    queryKey,
    queryFn: async ({ client }: { client: QueryClient }) => {
      const requestedUnder = historyLimit ?? null;
      let served: number | null | undefined;
      const rows = await api.getDashboardUsageBySource({
        days,
        project_id: projectId ?? undefined,
        tz,
        onHistoryLimit: (limit) => {
          served = limit;
        },
      });
      if (served !== undefined) {
        reportDashboardHistoryLimit(client, wsId, requestedUnder, served);
        // Rows cut to a stricter limit than this key promises must not be
        // cached under it.
        if (served !== null && isStricterHistoryLimit(served, requestedUnder)) {
          throw new DashboardHistoryLimitTightenedError(requestedUnder, served);
        }
      }
      return rows;
    },
    // Wait for the history limit, as every dashboard query does.
    enabled: !!wsId && historyLimit !== undefined,
    retry: (failureCount: number, error: Error) =>
      !isDashboardHistoryLimitTightened(error) && failureCount < 1,
    staleTime: 60 * 1000,
    refetchInterval: 5 * 60 * 1000,
    placeholderData: (previousData, previousQuery) => {
      const previousKey = previousQuery?.queryKey;
      const sameScope =
        !!previousKey &&
        previousKey.length === queryKey.length &&
        previousKey.every((part, index) => index === DAYS_INDEX || Object.is(part, queryKey[index]));
      return sameScope ? keepPreviousData(previousData) : undefined;
    },
  });
}
