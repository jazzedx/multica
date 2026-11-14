import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { dashboardKeys } from "./queries";

// Kept apart from queries.ts so the breakdown stays one self-contained
// addition. It follows the conventions documented there: the key shape, the
// rollup-cadence refresh, and carrying the previous result across a range
// change but never across a workspace, project or timezone change.
export const dashboardUsageBySourceKey = (
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) => [...dashboardKeys.all(wsId), "by-source", days, projectId, tz] as const;

const DAYS_INDEX = 3;

export function dashboardUsageBySourceOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardUsageBySourceKey(wsId, days, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardUsageBySource({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
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
