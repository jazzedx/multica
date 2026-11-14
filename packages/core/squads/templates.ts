import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

// Under the squads key, so anything that changes squads refetches the plans
// that describe them.
export const squadTemplateKeys = {
  all: (wsId: string) => [...workspaceKeys.squads(wsId), "templates"] as const,
  plan: (wsId: string, key: string, overwriteModifiedSkills: boolean) =>
    [...workspaceKeys.squads(wsId), "templates", key, "plan", overwriteModifiedSkills] as const,
  removalPlan: (wsId: string, key: string, removeModified: boolean) =>
    [...workspaceKeys.squads(wsId), "templates", key, "removal-plan", removeModified] as const,
};

export function squadTemplateListOptions(wsId: string) {
  return queryOptions({
    queryKey: squadTemplateKeys.all(wsId),
    queryFn: () => api.listSquadTemplates(),
    enabled: !!wsId,
    // The catalog only changes when the server is upgraded.
    staleTime: Infinity,
  });
}

/** What installing `key` would do in the workspace right now. */
export function squadTemplatePlanOptions(wsId: string, key: string, overwriteModifiedSkills: boolean) {
  return queryOptions({
    queryKey: squadTemplateKeys.plan(wsId, key, overwriteModifiedSkills),
    queryFn: () => api.planSquadTemplate(key, { overwrite_modified_skills: overwriteModifiedSkills }),
    enabled: !!wsId && !!key,
    // Agents and skills change it too, and they live under other keys.
    staleTime: 0,
  });
}

/** What uninstalling `key` would remove from the workspace right now. */
export function squadTemplateRemovalPlanOptions(wsId: string, key: string, removeModified: boolean) {
  return queryOptions({
    queryKey: squadTemplateKeys.removalPlan(wsId, key, removeModified),
    queryFn: () => api.planSquadTemplateRemoval(key, { remove_modified: removeModified }),
    enabled: !!wsId && !!key,
    staleTime: 0,
  });
}
