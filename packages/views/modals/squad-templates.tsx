"use client";

import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2 } from "@multica/ui/icons";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeListOptions } from "@multica/core/runtimes";
import {
  squadTemplateListOptions,
  squadTemplatePlanOptions,
  squadTemplateRemovalPlanOptions,
} from "@multica/core/squads";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type {
  SquadTemplate,
  SquadTemplateAction,
  SquadTemplatePlan,
  SquadTemplateRemoval,
  SquadTemplateRemovalPlan,
  SquadTemplateText,
} from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";

import { RuntimePicker } from "../agents/components/runtime-picker";
import { useNavigation } from "../navigation";
import { useLocale, useT } from "../i18n";

/** The catalog carries Chinese and English; every other UI language reads English. */
function localized(text: SquadTemplateText, locale: string): string {
  return locale.startsWith("zh") ? text.zh || text.en : text.en || text.zh;
}

function ActionLabel({ action }: { action: SquadTemplateAction }) {
  const { t } = useT("modals");
  const known: Record<string, { label: string; className: string }> = {
    create: { label: t(($) => $.squad_templates.action.create), className: "text-primary" },
    update: { label: t(($) => $.squad_templates.action.update), className: "text-primary" },
    restore: { label: t(($) => $.squad_templates.action.restore), className: "text-primary" },
    unchanged: { label: t(($) => $.squad_templates.action.unchanged), className: "text-muted-foreground" },
    exists: { label: t(($) => $.squad_templates.action.exists), className: "text-muted-foreground" },
    keep: { label: t(($) => $.squad_templates.action.keep), className: "text-warning" },
  };
  // An action from a newer server is shown as sent rather than hidden.
  const { label, className } = known[action] ?? { label: action, className: "text-muted-foreground" };
  return <span className={cn("shrink-0 text-caption", className)}>{label}</span>;
}

function RemovalLabel({ item }: { item: SquadTemplateRemoval }) {
  const { t } = useT("modals");
  if (item.action === "keep") {
    const reasons: Record<string, string> = {
      modified: t(($) => $.squad_templates.uninstall.reason.modified),
      in_squad: t(($) => $.squad_templates.uninstall.reason.in_squad),
      open_issues: t(($) => $.squad_templates.uninstall.reason.open_issues),
      in_use: t(($) => $.squad_templates.uninstall.reason.in_use),
      not_installed: t(($) => $.squad_templates.uninstall.reason.not_installed),
    };
    const reason = item.reason ? (reasons[item.reason] ?? item.reason) : "";
    return (
      <span
        className={cn(
          "shrink-0 text-caption",
          // "modified" is the one reason the viewer can overrule.
          item.reason === "modified" ? "text-warning" : "text-muted-foreground",
        )}
      >
        {reason
          ? t(($) => $.squad_templates.uninstall.kept_because, { reason })
          : t(($) => $.squad_templates.uninstall.action.keep)}
      </span>
    );
  }
  const known: Record<string, { label: string; className: string }> = {
    archive: { label: t(($) => $.squad_templates.uninstall.action.archive), className: "text-destructive" },
    delete: { label: t(($) => $.squad_templates.uninstall.action.delete), className: "text-destructive" },
    absent: { label: t(($) => $.squad_templates.uninstall.action.absent), className: "text-muted-foreground" },
  };
  const { label, className } = known[item.action] ?? { label: item.action, className: "text-muted-foreground" };
  return <span className={cn("shrink-0 text-caption", className)}>{label}</span>;
}

function PlanRow({ name, detail, status }: { name: string; detail?: string; status: ReactNode }) {
  return (
    <li className="flex items-center gap-2 py-1">
      <span className="min-w-0 truncate text-body">{name}</span>
      {detail ? (
        <span className="min-w-0 flex-1 truncate text-caption text-muted-foreground">{detail}</span>
      ) : (
        <span className="flex-1" />
      )}
      {status}
    </li>
  );
}

function PlanSection({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <section aria-label={title}>
      <h3 className="text-caption font-medium text-muted-foreground">
        {title}
        {count === undefined ? null : <span className="ml-1.5 tabular-nums">{count}</span>}
      </h3>
      <ul className="mt-1 divide-y divide-border/60">{children}</ul>
    </section>
  );
}

function PlanError({ error, fallback, onRetry }: { error: unknown; fallback: string; onRetry: () => void }) {
  const { t } = useT("modals");
  return (
    <div className="flex items-center gap-3">
      <p role="alert" className="min-w-0 flex-1 text-body text-destructive">
        {error instanceof Error ? error.message : fallback}
      </p>
      <Button type="button" variant="outline" size="sm" onClick={onRetry}>
        {t(($) => $.squad_templates.retry)}
      </Button>
    </div>
  );
}

/** True when an install would change nothing. */
function isSetUp(plan: SquadTemplatePlan): boolean {
  return (
    plan.skills.every((s) => s.action === "unchanged" || s.action === "keep") &&
    plan.agents.every((a) => a.action === "exists" && a.add_skills === 0) &&
    plan.squad.action === "exists" &&
    plan.squad.add_members === 0
  );
}

/** True when the workspace holds anything active under the template's names. */
function hasFootprint(plan: SquadTemplatePlan): boolean {
  return (
    plan.squad.action !== "create" ||
    plan.agents.some((a) => a.action === "exists") ||
    plan.skills.some((s) => s.action !== "create")
  );
}

/** True when an uninstall would change something. */
function removesAnything(plan: SquadTemplateRemovalPlan): boolean {
  return [plan.squad, ...plan.agents, ...plan.skills].some(
    (item) => item.action === "archive" || item.action === "delete",
  );
}

const BODY_CLASS = "min-h-0 flex-1 space-y-5 overflow-y-auto p-5";
const FOOTER_CLASS = "mx-0 mb-0 px-5 py-3";

function useRefreshWorkspace(wsId: string) {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: workspaceKeys.squads(wsId) });
    void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    void queryClient.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
  };
}

function InstallPanel({
  templateKey,
  picker,
  onBusyChange,
  onUninstall,
  onClose,
}: {
  templateKey: string;
  picker: ReactNode;
  onBusyChange: (busy: boolean) => void;
  onUninstall: () => void;
  onClose: () => void;
}) {
  const { t } = useT("modals");
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const router = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const refreshWorkspace = useRefreshWorkspace(wsId);

  const [overwrite, setOverwrite] = useState(false);
  const planQuery = useQuery({
    ...squadTemplatePlanOptions(wsId, templateKey, overwrite),
    // Ticking the overwrite box asks for a new plan of the same template; keep
    // showing the current one meanwhile so the box does not vanish under the
    // pointer. Another template's plan is never shown in its place.
    placeholderData: (previous) => (previous?.template === templateKey ? previous : undefined),
  });
  const plan = planQuery.data;

  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [runtimeId, setRuntimeId] = useState("");

  const needsRuntime = !!plan?.agents.some((a) => a.action === "create");
  const setUp = !!plan && isSetUp(plan);
  const hasKeptSkills = !!plan?.skills.some((s) => s.action === "keep");

  const install = useMutation({
    mutationFn: () =>
      api.installSquadTemplate(templateKey, {
        runtime_id: needsRuntime ? runtimeId : undefined,
        overwrite_modified_skills: overwrite || undefined,
      }),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (result) => {
      refreshWorkspace();
      onClose();
      toast.success(t(($) => $.squad_templates.toast_installed));
      if (result.squad.id) router.push(wsPaths.squadDetail(result.squad.id));
    },
    onError: (err) => {
      // A failed install keeps what it finished; the refreshed plan shows the rest.
      refreshWorkspace();
      toast.error(err instanceof Error ? err.message : t(($) => $.squad_templates.toast_failed));
    },
  });

  const canInstall = !!plan && !setUp && (!needsRuntime || !!runtimeId) && !install.isPending;

  return (
    <>
      <div className={BODY_CLASS}>
        {picker}

        {templateKey && planQuery.isLoading ? (
          <Loader2 className="mx-auto size-4 animate-spin text-muted-foreground" />
        ) : null}
        {templateKey && planQuery.isError ? (
          <PlanError
            error={planQuery.error}
            fallback={t(($) => $.squad_templates.plan_failed)}
            onRetry={() => void planQuery.refetch()}
          />
        ) : null}

        {plan ? (
          <>
            {needsRuntime ? (
              <div>
                <RuntimePicker
                  runtimes={runtimes}
                  runtimesLoading={runtimesLoading}
                  members={members}
                  currentUserId={currentUserId}
                  selectedRuntimeId={runtimeId}
                  onSelect={setRuntimeId}
                  disabled={install.isPending}
                />
                <p className="mt-1.5 text-caption text-muted-foreground">
                  {t(($) => $.squad_templates.runtime_hint)}
                </p>
              </div>
            ) : null}

            {setUp ? (
              <p className="text-body text-muted-foreground">
                {t(($) => $.squad_templates.already_set_up)}
              </p>
            ) : null}

            <PlanSection title={t(($) => $.squad_templates.section_squad)}>
              <PlanRow
                name={plan.squad.name}
                detail={
                  plan.squad.add_members > 0
                    ? t(($) => $.squad_templates.add_members, { n: plan.squad.add_members })
                    : undefined
                }
                status={<ActionLabel action={plan.squad.action} />}
              />
            </PlanSection>

            <PlanSection title={t(($) => $.squad_templates.section_agents)} count={plan.agents.length}>
              {plan.agents.map((agent) => (
                <PlanRow
                  key={agent.name}
                  name={agent.name}
                  detail={
                    agent.add_skills > 0
                      ? t(($) => $.squad_templates.add_skills, { n: agent.add_skills })
                      : undefined
                  }
                  status={<ActionLabel action={agent.action} />}
                />
              ))}
            </PlanSection>

            <div>
              <PlanSection title={t(($) => $.squad_templates.section_skills)} count={plan.skills.length}>
                {plan.skills.map((skill) => (
                  <PlanRow key={skill.name} name={skill.name} status={<ActionLabel action={skill.action} />} />
                ))}
              </PlanSection>
              {hasKeptSkills || overwrite ? (
                <Label className="mt-3 flex items-center gap-2 text-body font-normal">
                  <Checkbox
                    checked={overwrite}
                    disabled={install.isPending}
                    onCheckedChange={(checked) => setOverwrite(checked === true)}
                  />
                  {t(($) => $.squad_templates.overwrite_label)}
                </Label>
              ) : null}
            </div>
          </>
        ) : null}
      </div>

      <DialogFooter className={FOOTER_CLASS}>
        {plan && hasFootprint(plan) ? (
          <Button
            type="button"
            variant="ghost"
            className="sm:mr-auto"
            disabled={install.isPending}
            onClick={onUninstall}
          >
            {t(($) => $.squad_templates.uninstall.open)}
          </Button>
        ) : null}
        <Button type="button" variant="outline" disabled={install.isPending} onClick={onClose}>
          {t(($) => $.squad_templates.cancel)}
        </Button>
        {setUp && plan?.squad.id ? (
          <Button
            type="button"
            onClick={() => {
              onClose();
              if (plan.squad.id) router.push(wsPaths.squadDetail(plan.squad.id));
            }}
          >
            {t(($) => $.squad_templates.open_squad)}
          </Button>
        ) : (
          <Button
            type="button"
            disabled={!canInstall}
            aria-busy={install.isPending}
            onClick={() => install.mutate()}
          >
            {install.isPending ? (
              <>
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                {t(($) => $.squad_templates.installing)}
              </>
            ) : (
              t(($) => $.squad_templates.install)
            )}
          </Button>
        )}
      </DialogFooter>
    </>
  );
}

function UninstallPanel({
  templateKey,
  onBusyChange,
  onBack,
  onClose,
}: {
  templateKey: string;
  onBusyChange: (busy: boolean) => void;
  onBack: () => void;
  onClose: () => void;
}) {
  const { t } = useT("modals");
  const wsId = useWorkspaceId();
  const refreshWorkspace = useRefreshWorkspace(wsId);

  const [removeModified, setRemoveModified] = useState(false);
  const planQuery = useQuery({
    ...squadTemplateRemovalPlanOptions(wsId, templateKey, removeModified),
    placeholderData: (previous) => (previous?.template === templateKey ? previous : undefined),
  });
  const plan = planQuery.data;
  const hasModified =
    !!plan && [plan.squad, ...plan.agents, ...plan.skills].some((item) => item.reason === "modified");

  const uninstall = useMutation({
    mutationFn: () => api.uninstallSquadTemplate(templateKey, { remove_modified: removeModified || undefined }),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: () => {
      refreshWorkspace();
      onClose();
      toast.success(t(($) => $.squad_templates.uninstall.toast_done));
    },
    onError: (err) => {
      // A failed uninstall keeps what it finished; the refreshed plan shows the rest.
      refreshWorkspace();
      toast.error(err instanceof Error ? err.message : t(($) => $.squad_templates.uninstall.toast_failed));
    },
  });

  return (
    <>
      <div className={BODY_CLASS}>
        {planQuery.isLoading ? (
          <Loader2 className="mx-auto size-4 animate-spin text-muted-foreground" />
        ) : null}
        {planQuery.isError ? (
          <PlanError
            error={planQuery.error}
            fallback={t(($) => $.squad_templates.uninstall.plan_failed)}
            onRetry={() => void planQuery.refetch()}
          />
        ) : null}

        {plan ? (
          <>
            {removesAnything(plan) ? null : (
              <p className="text-body text-muted-foreground">
                {t(($) => $.squad_templates.uninstall.nothing)}
              </p>
            )}

            <PlanSection title={t(($) => $.squad_templates.section_squad)}>
              <PlanRow name={plan.squad.name} status={<RemovalLabel item={plan.squad} />} />
            </PlanSection>
            <PlanSection title={t(($) => $.squad_templates.section_agents)} count={plan.agents.length}>
              {plan.agents.map((agent) => (
                <PlanRow key={agent.name} name={agent.name} status={<RemovalLabel item={agent} />} />
              ))}
            </PlanSection>
            <PlanSection title={t(($) => $.squad_templates.section_skills)} count={plan.skills.length}>
              {plan.skills.map((skill) => (
                <PlanRow key={skill.name} name={skill.name} status={<RemovalLabel item={skill} />} />
              ))}
            </PlanSection>

            {hasModified || removeModified ? (
              <Label className="flex items-center gap-2 text-body font-normal">
                <Checkbox
                  checked={removeModified}
                  disabled={uninstall.isPending}
                  onCheckedChange={(checked) => setRemoveModified(checked === true)}
                />
                {t(($) => $.squad_templates.uninstall.remove_modified_label)}
              </Label>
            ) : null}
          </>
        ) : null}
      </div>

      <DialogFooter className={FOOTER_CLASS}>
        <Button type="button" variant="outline" disabled={uninstall.isPending} onClick={onBack}>
          {t(($) => $.squad_templates.uninstall.back)}
        </Button>
        <Button
          type="button"
          variant="destructive"
          disabled={!plan || !removesAnything(plan) || uninstall.isPending}
          aria-busy={uninstall.isPending}
          onClick={() => uninstall.mutate()}
        >
          {uninstall.isPending ? (
            <>
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t(($) => $.squad_templates.uninstall.confirming)}
            </>
          ) : (
            t(($) => $.squad_templates.uninstall.confirm)
          )}
        </Button>
      </DialogFooter>
    </>
  );
}

export function SquadTemplatesModal({
  open = true,
  onClose,
  onOpenChangeComplete,
}: {
  open?: boolean;
  onClose: () => void;
  onOpenChangeComplete?: (open: boolean) => void;
}) {
  const { t } = useT("modals");
  const locale = useLocale();
  const wsId = useWorkspaceId();

  const templatesQuery = useQuery(squadTemplateListOptions(wsId));
  const templates: SquadTemplate[] = templatesQuery.data ?? [];
  const [pickedKey, setPickedKey] = useState("");
  const active =
    templates.find((tpl) => tpl.key === pickedKey) ??
    templates.find((tpl) => tpl.recommended) ??
    templates[0];
  const activeKey = active?.key ?? "";

  const [uninstalling, setUninstalling] = useState(false);
  const [busy, setBusy] = useState(false);

  const picker = templatesQuery.isLoading ? (
    <Loader2 className="mx-auto size-4 animate-spin text-muted-foreground" />
  ) : templates.length === 0 ? (
    <p className="text-body text-muted-foreground">{t(($) => $.squad_templates.empty)}</p>
  ) : (
    <div className="space-y-2">
      {templates.map((tpl) => (
        <button
          key={tpl.key}
          type="button"
          aria-pressed={tpl.key === activeKey}
          disabled={busy}
          onClick={() => setPickedKey(tpl.key)}
          className={cn(
            "w-full rounded-lg border px-3 py-2.5 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
            tpl.key === activeKey ? "border-primary bg-primary/5" : "border-border hover:bg-muted/50",
          )}
        >
          <span className="flex items-center gap-2">
            <span className="min-w-0 truncate text-body font-medium">{localized(tpl.title, locale)}</span>
            {tpl.recommended ? (
              <span className="shrink-0 rounded-md bg-primary/10 px-1.5 py-0.5 text-micro font-medium text-primary">
                {t(($) => $.squad_templates.recommended)}
              </span>
            ) : null}
            <span className="ml-auto shrink-0 text-caption text-muted-foreground tabular-nums">
              {t(($) => $.squad_templates.counts, {
                agents: tpl.agents.length,
                skills: tpl.skills.length,
              })}
            </span>
          </span>
          <span className="mt-1 block text-caption text-muted-foreground">{localized(tpl.summary, locale)}</span>
        </button>
      ))}
    </div>
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) onClose();
      }}
      onOpenChangeComplete={onOpenChangeComplete}
    >
      <DialogContent className="flex max-h-[85%] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader className="border-b px-5 py-3">
          <DialogTitle className="text-title-sm font-semibold">
            {uninstalling && active
              ? t(($) => $.squad_templates.uninstall.title, { name: localized(active.title, locale) })
              : t(($) => $.squad_templates.title)}
          </DialogTitle>
          <DialogDescription>
            {uninstalling
              ? t(($) => $.squad_templates.uninstall.description)
              : t(($) => $.squad_templates.description)}
          </DialogDescription>
        </DialogHeader>

        {/* Keyed by template so a panel never carries one template's choices into another. */}
        {uninstalling ? (
          <UninstallPanel
            key={activeKey}
            templateKey={activeKey}
            onBusyChange={setBusy}
            onBack={() => setUninstalling(false)}
            onClose={onClose}
          />
        ) : (
          <InstallPanel
            key={activeKey}
            templateKey={activeKey}
            picker={picker}
            onBusyChange={setBusy}
            onUninstall={() => setUninstalling(true)}
            onClose={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
