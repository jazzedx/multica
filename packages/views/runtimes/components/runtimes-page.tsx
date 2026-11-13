"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  Check,
  Laptop,
  Loader2,
  Plus,
  Server,
  SquareTerminal,
} from "@multica/ui/icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  memberNeedsMikaSetup,
  useBootstrapMika,
} from "@multica/core/onboarding";
import { MIKA_PLACEHOLDER_EMOJI } from "../../onboarding/components/mika-intro";
import {
  useRequiredWorkspaceSlug,
  useWorkspacePaths,
} from "@multica/core/paths";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import { runtimeProfileListOptions } from "@multica/core/runtimes";
import {
  runtimeListOptions,
  runtimeKeys,
} from "@multica/core/runtimes/queries";
import { useWSEvent } from "@multica/core/realtime";
import {
  agentListOptions,
  memberListOptions,
} from "@multica/core/workspace/queries";
import type { AgentRuntime, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  MikaRuntimeChoice,
  type MikaRuntimeSelection,
} from "./mika-runtime-choice";
import {
  CollectionCreateButton,
  CollectionNoMatches,
  CollectionPageHeader,
  CollectionScopeItems,
} from "../../layout/collection-page";
import {
  CollectionEmptyValue,
  CollectionList,
  CollectionNameCell,
  CollectionTag,
  type CollectionColumn,
  type CollectionGroup,
} from "../../layout/collection-list";
import { cn } from "@multica/ui/lib/utils";
import { useNavigation } from "../../navigation";
import { useRestoredScrollRef } from "../../platform/scroll-restoration";
import { getMikaOnboarding, pickContentLang } from "../../onboarding/templates";
import { ConnectRemoteDialog } from "./connect-remote-dialog";
import { RuntimeList, buildWorkloadIndex } from "./runtime-list";
import { RuntimesIntro } from "./runtimes-intro";
import { pendingRuntimeFromProfile } from "./pending-runtime";
import { buildRuntimeMachines, type RuntimeMachine } from "./runtime-machines";
import {
  HealthIcon,
  ProviderIconStack,
  machineIcon,
  useHealthLabel,
} from "./shared";
import { useT, useTimeAgo } from "../../i18n";

export interface RuntimesPageProps {
  /** Desktop-only daemon id used to identify this device. */
  localDaemonId?: string | null;
  /** Desktop-only friendly device name for the local daemon. */
  localMachineName?: string | null;
  /** Keep the local device visible even before its first runtime registers. */
  hasLocalMachine?: boolean;
  /** The bundled daemon is starting but has not registered yet. */
  bootstrapping?: boolean;
}

type StatusFilter = "all" | "online" | "offline";
const STATUS_FILTERS: StatusFilter[] = ["all", "online", "offline"];

type RuntimeRow =
  | { kind: "machine"; id: string; machine: RuntimeMachine; agentCount: number }
  | {
      kind: "action";
      id: string;
      icon: typeof Server;
      title: string;
      description: string;
      action: ReactNode;
    };

function useNowTick(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

export function RuntimesPage({
  localDaemonId,
  localMachineName,
  hasLocalMachine,
  bootstrapping,
}: RuntimesPageProps = {}) {
  const { t } = useT("runtimes");
  const isAuthLoading = useAuthStore((state) => state.isLoading);
  const currentUserId = useAuthStore((state) => state.user?.id);
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const paths = useWorkspacePaths();
  const [showConnectDialog, setShowConnectDialog] = useState(false);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const restoreScrollRef = useRestoredScrollRef("runtimes");

  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );
  const { data: runtimeProfiles = [], isLoading: profilesLoading } = useQuery(
    runtimeProfileListOptions(wsId),
  );
  const { data: agents = [], isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  // The Mika entrypoint is per member, not per workspace: the agent alone does
  // not say whether *this* member's conversation was ever opened and kicked
  // off. See memberNeedsMikaSetup.
  const { data: chatSessions = [], isLoading: chatSessionsLoading } = useQuery(
    chatSessionsOptions(wsId),
  );

  const handleDaemonEvent = useCallback(() => {
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  }, [qc, wsId]);
  useWSEvent("daemon:register", handleDaemonEvent);

  const workloadIndex = useMemo(
    () => buildWorkloadIndex(agents, snapshot),
    [agents, snapshot],
  );
  const agentCountByRuntime = useMemo(() => {
    const counts = new Map<string, number>();
    for (const agent of agents) {
      if (!agent.runtime_id) continue;
      counts.set(agent.runtime_id, (counts.get(agent.runtime_id) ?? 0) + 1);
    }
    return counts;
  }, [agents]);
  const now = useNowTick();
  const machines = useMemo(
    () =>
      buildRuntimeMachines(runtimes, {
        now,
        localDaemonId,
        localMachineName,
        currentUserId,
        workloadByRuntimeId: workloadIndex,
        ensureLocalMachine: hasLocalMachine,
      }),
    [
      runtimes,
      now,
      localDaemonId,
      localMachineName,
      currentUserId,
      workloadIndex,
      hasLocalMachine,
    ],
  );
  const orphanProfileRuntimes = useMemo(() => {
    if (machines.some((machine) => machine.mode === "local")) return [];
    return runtimeProfiles.map((profile) => {
      const createdAt = Date.parse(profile.created_at);
      return pendingRuntimeFromProfile({
        profile,
        createdAt: Number.isFinite(createdAt) ? createdAt : 0,
        fallbackMachineName: "Unassigned",
      });
    });
  }, [machines, runtimeProfiles]);

  // The header, toolbar and column headers render through loading; only the
  // rows wait, so nothing above them moves when the list lands.
  const listLoading = isAuthLoading || runtimesLoading || profilesLoading;

  const isDesktop = hasLocalMachine === true;
  const machineList = machines.filter(
    (machine) =>
      !(
        machine.runtimes.length > 0 &&
        machine.runtimes.every((runtime) => runtime.provider === "qoder_cloud")
      ),
  );
  const localMachine = machineList.find((machine) => machine.isCurrent);

  const filtering = statusFilter !== "all";
  const matchesStatus = (online: boolean) =>
    statusFilter === "all" || (statusFilter === "online") === online;

  const visibleMachines = machineList.filter((machine) =>
    matchesStatus(machine.health === "online"),
  );
  const showAddMachineRow = !filtering && machineList.length === 0;
  const showIntro =
    !listLoading &&
    !filtering &&
    !isDesktop &&
    machineList.length === 0 &&
    orphanProfileRuntimes.length === 0;
  const nothingMatches = filtering && visibleMachines.length === 0;

  const totalCount = machineList.length;
  const onlineCount = machineList.filter((machine) => machine.health === "online").length;
  const statusCounts: Record<StatusFilter, number> = {
    all: totalCount,
    online: onlineCount,
    offline: totalCount - onlineCount,
  };

  const scopeLabels: Record<StatusFilter, string> = {
    all: t(($) => $.page.filters.all),
    online: t(($) => $.page.filters.online),
    offline: t(($) => $.page.filters.offline),
  };

  const addMenuProps = {
    isDesktop,
    localMachineName: localMachineName ?? localMachine?.title ?? null,
    onAddMachine: () => setShowConnectDialog(true),
  };

  const columns = useMemo<CollectionColumn<RuntimeRow>[]>(
    () => [
      {
        key: "name",
        header: t(($) => $.page.columns.name),
        cell: (row) => (
          <RuntimeNameCell
            row={row}
            currentUserId={currentUserId ?? null}
            members={members}
          />
        ),
      },
      {
        key: "harness",
        header: t(($) => $.page.columns.harness),
        width: 112,
        cell: (row) =>
          row.kind === "machine" ? (
            <ProviderIconStack providers={row.machine.providerNames} />
          ) : null,
      },
      {
        key: "agents",
        header: t(($) => $.page.columns.agents),
        width: 56,
        align: "end",
        className: "tabular-nums",
        cell: (row) => (row.kind === "machine" ? row.agentCount : null),
      },
      {
        key: "workload",
        header: t(($) => $.page.columns.workload),
        width: 112,
        cell: (row) => <RuntimeWorkloadCell row={row} />,
      },
      {
        key: "status",
        header: t(($) => $.page.columns.status),
        width: 136,
        core: true,
        cell: (row) => <RuntimeStatusCell row={row} bootstrapping={bootstrapping} />,
      },
    ],
    [t, currentUserId, members, bootstrapping],
  );

  const machineRows: RuntimeRow[] = visibleMachines.map((machine) => ({
    kind: "machine",
    id: machine.id,
    machine,
    agentCount: machine.runtimes.reduce(
      (sum, runtime) => sum + (agentCountByRuntime.get(runtime.id) ?? 0),
      0,
    ),
  }));
  if (showAddMachineRow) {
    machineRows.push({
      kind: "action",
      id: "action:add-machine",
      icon: SquareTerminal,
      title: t(($) => $.page.rows.add_machine_title),
      description: t(($) => $.page.rows.add_machine_hint),
      action: (
        <Button variant="outline" size="xs" onClick={() => setShowConnectDialog(true)}>
          {t(($) => $.page.rows.view_commands)}
        </Button>
      ),
    });
  }
  const groups: CollectionGroup<RuntimeRow>[] = nothingMatches
    ? []
    : [
        {
          key: "machines",
          label: t(($) => $.page.groups.machines.title),
          count: machineList.length,
          rows: machineRows,
        },
      ];
  const rowHref = (row: RuntimeRow) =>
    row.kind === "machine" ? paths.runtimeDetail(row.machine.id) : null;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <CollectionPageHeader
        icon={Server}
        title={scopeLabels[statusFilter]}
        menu={
          <CollectionScopeItems
            value={statusFilter}
            onChange={setStatusFilter}
            options={STATUS_FILTERS.map((filter) => ({
              value: filter,
              label: scopeLabels[filter],
              count: statusCounts[filter],
            }))}
          />
        }
        actions={<AddRuntimeMenu {...addMenuProps} />}
      />

      {showIntro ? (
        <RuntimesIntro
          action={
            <AddRuntimeMenu
              {...addMenuProps}
              align="start"
              trigger={
                <Button>
                  <Plus aria-hidden="true" />
                  {t(($) => $.page.add)}
                </Button>
              }
            />
          }
        />
      ) : (
        <div
          ref={restoreScrollRef}
          data-tab-scroll-root="runtimes"
          className="min-h-0 flex-1 overflow-y-auto"
        >
          <CollectionList
            embedded
            pageShortcuts
            label={scopeLabels[statusFilter]}
            columns={columns}
            groups={groups}
            loading={listLoading}
            getRowId={(row) => row.id}
            getRowHref={rowHref}
            getRowTitle={(row) => (row.kind === "machine" ? row.machine.title : undefined)}
            empty={
              <CollectionNoMatches
                title={t(($) => $.page.rows.no_matches)}
                onReset={() => setStatusFilter("all")}
                hasFilters={filtering}
              />
            }
          />
          <div className="flex flex-col gap-6 px-6 pt-6 pb-16 empty:hidden">
            {!agentsLoading &&
              !chatSessionsLoading &&
              memberNeedsMikaSetup(agents, chatSessions) &&
              runtimes.some((runtime) => runtime.provider !== "qoder_cloud") && (
                <MikaSetupCard
                  workspaceId={wsId}
                  runtimes={runtimes.filter(
                    (runtime) => runtime.provider !== "qoder_cloud",
                  )}
                  runtimesLoading={runtimesLoading}
                  currentUserId={currentUserId ?? null}
                />
              )}
            {orphanProfileRuntimes.length > 0 && (
              <OrphanRuntimeProfiles
                runtimes={orphanProfileRuntimes}
                now={now}
              />
            )}
          </div>
        </div>
      )}

      {showConnectDialog && (
        <ConnectRemoteDialog onClose={() => setShowConnectDialog(false)} />
      )}
    </div>
  );
}

function AddRuntimeMenu({
  isDesktop,
  localMachineName,
  onAddMachine,
  trigger,
  align = "end",
}: {
  isDesktop: boolean;
  localMachineName: string | null;
  onAddMachine: () => void;
  trigger?: ReactElement;
  align?: "start" | "end";
}) {
  const { t } = useT("runtimes");
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={trigger ?? <CollectionCreateButton label={t(($) => $.page.add)} />}
      />
      <DropdownMenuContent align={align} className="w-80">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t(($) => $.page.add_menu.machines)}</DropdownMenuLabel>
          {isDesktop && (
            <AddMenuItem
              icon={<Laptop className="size-4 text-muted-foreground" />}
              title={t(($) => $.page.add_menu.this_computer)}
              description={
                localMachineName
                  ? t(($) => $.page.add_menu.this_computer_connected, {
                      name: localMachineName,
                    })
                  : undefined
              }
              trailing={<Check className="size-4 text-muted-foreground" />}
              disabled
            />
          )}
          <AddMenuItem
            icon={<SquareTerminal className="size-4 text-muted-foreground" />}
            title={t(($) => $.page.add_menu.other_machine)}
            description={t(($) => $.page.add_menu.other_machine_hint)}
            onClick={onAddMachine}
          />
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AddMenuItem({
  icon,
  title,
  description,
  trailing,
  ...props
}: {
  icon: ReactNode;
  title: string;
  description?: string;
  trailing?: ReactNode;
} & Omit<React.ComponentProps<typeof DropdownMenuItem>, "children" | "title">) {
  return (
    <DropdownMenuItem className="gap-2.5 px-2 py-1.5" {...props}>
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-background">
        {icon}
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-body font-medium">{title}</span>
        {description && (
          <span className="truncate text-caption text-muted-foreground">
            {description}
          </span>
        )}
      </span>
      {trailing}
    </DropdownMenuItem>
  );
}

function RuntimeNameCell({
  row,
  currentUserId,
  members,
}: {
  row: RuntimeRow;
  currentUserId: string | null;
  members: MemberWithUser[];
}) {
  const { t } = useT("runtimes");
  switch (row.kind) {
    case "machine": {
      const { machine } = row;
      const Icon = machineIcon(machine);
      const ownerId = machine.runtimes[0]?.owner_id ?? null;
      const owner =
        ownerId && ownerId === currentUserId
          ? t(($) => $.page.rows.owner_you)
          : (members.find((member) => member.user_id === ownerId)?.name ?? null);
      const description =
        [machine.deviceSummary, owner].filter(Boolean).join(" · ") ||
        (machine.section === "cloud"
          ? t(($) => $.machine.metrics.cloud_worker)
          : t(($) => $.machine.metrics.local_daemon));
      return (
        <CollectionNameCell
          icon={<Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />}
          title={machine.title}
          badges={
            machine.isCurrent ? (
              <CollectionTag>{t(($) => $.machine.this_machine)}</CollectionTag>
            ) : undefined
          }
          description={description}
        />
      );
    }
    case "action": {
      const Icon = row.icon;
      return (
        <CollectionNameCell
          icon={
            <span className="flex size-5 shrink-0 items-center justify-center rounded-sm border border-dashed text-muted-foreground">
              <Icon aria-hidden="true" className="size-3" />
            </span>
          }
          title={row.title}
          description={row.description}
          trailing={<span className="shrink-0">{row.action}</span>}
        />
      );
    }
  }
}

function WorkloadText({
  running,
  queued,
}: {
  running: number;
  queued: number;
}) {
  const { t } = useT("runtimes");
  if (running + queued === 0) return <>{t(($) => $.machine.metrics.workload_idle)}</>;
  return (
    <span className="truncate tabular-nums text-foreground">
      {t(($) => $.machine.metrics.workload_hint, { running, queued })}
    </span>
  );
}

function RuntimeWorkloadCell({ row }: { row: RuntimeRow }) {
  if (row.kind === "machine") {
    return row.machine.health === "online" ? (
      <WorkloadText running={row.machine.runningCount} queued={row.machine.queuedCount} />
    ) : (
      <CollectionEmptyValue />
    );
  }
  return null;
}

function RuntimeStatusCell({
  row,
  bootstrapping,
}: {
  row: RuntimeRow;
  bootstrapping?: boolean;
}) {
  const { t } = useT("runtimes");
  const healthLabel = useHealthLabel();
  const timeAgo = useTimeAgo();
  switch (row.kind) {
    case "machine": {
      const { machine } = row;
      if (machine.isCurrent && bootstrapping && machine.runtimes.length === 0) {
        return (
          <>
            <Loader2 aria-hidden="true" className="size-3 shrink-0 animate-spin" />
            <span className="truncate">{t(($) => $.page.bootstrapping.title)}</span>
          </>
        );
      }
      const online = machine.health === "online";
      return (
        <>
          <HealthIcon health={machine.health} />
          <span className={cn("truncate", online && "text-foreground")}>
            {healthLabel(machine.health)}
            {!online && machine.lastSeenAt ? ` · ${timeAgo(machine.lastSeenAt)}` : ""}
          </span>
        </>
      );
    }
    case "action":
      return null;
  }
}

/**
 * Entry point for creating Mika once a runtime exists.
 *
 * The action opens a picker rather than provisioning straight away. It used to
 * take `runtimes.find(online) ?? runtimes[0]` and create Mika on it silently —
 * but one machine commonly exposes every agent CLI it has installed (nine, on
 * the box this was reported from), so "the first online one" is arbitrary and
 * could well be a CLI the member never intended to run their Chief of Staff
 * on. Onboarding already makes this an explicit choice; this is the same
 * decision reached from a different entry point, so it asks the same way and
 * reuses the same two controls.
 */
function MikaSetupCard({
  workspaceId,
  runtimes,
  runtimesLoading,
  currentUserId,
}: {
  workspaceId: string;
  runtimes: AgentRuntime[];
  runtimesLoading?: boolean;
  currentUserId: string | null;
}) {
  const { t, i18n } = useT("runtimes");
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const wsSlug = useRequiredWorkspaceSlug();
  const bootstrapMika = useBootstrapMika(workspaceId);

  const [open, setOpen] = useState(false);
  // Seeded with the old heuristic so the dialog opens on a sensible default;
  // the point is that it is now visible and changeable, not that it is unset.
  const defaultRuntimeId =
    runtimes.find((runtime) => runtime.status === "online")?.id ??
    runtimes[0]?.id ??
    "";
  const [choice, setChoice] = useState<MikaRuntimeSelection | null>(null);

  const value: MikaRuntimeSelection = choice ?? {
    runtimeId: defaultRuntimeId,
    model: "",
  };
  const runtimeId = value.runtimeId;

  const handleStart = async () => {
    if (!runtimeId || bootstrapMika.isPending) return;
    const lang = pickContentLang(i18n.language);
    try {
      const result = await bootstrapMika.mutateAsync({
        workspaceSlug: wsSlug,
        runtimeId,
        model: value.model || undefined,
        ...getMikaOnboarding(lang),
      });
      setOpen(false);
      navigation.push(paths.chatSession(result.chatSession.id));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.mika_setup.failed),
      );
    }
  };

  return (
    <>
      <div className="flex flex-col gap-4 rounded-xl border bg-card p-5 sm:flex-row sm:items-center">
        <span
          role="img"
          aria-label={t(($) => $.mika_setup.title)}
          className="flex size-10 shrink-0 select-none items-center justify-center rounded-full bg-muted text-title-lg leading-none"
        >
          {MIKA_PLACEHOLDER_EMOJI}
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-body font-semibold">
            {t(($) => $.mika_setup.title)}
          </h2>
          <p className="mt-1 text-body leading-relaxed text-muted-foreground">
            {t(($) => $.mika_setup.description)}
          </p>
        </div>
        <Button className="shrink-0" onClick={() => setOpen(true)}>
          {t(($) => $.mika_setup.action)}
        </Button>
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-[480px]">
          <DialogHeader>
            <DialogTitle>{t(($) => $.mika_setup.dialog_title)}</DialogTitle>
          </DialogHeader>

          <MikaRuntimeChoice
            runtimes={runtimes}
            runtimesLoading={runtimesLoading}
            currentUserId={currentUserId}
            value={value}
            onChange={setChoice}
            disabled={bootstrapMika.isPending}
          />

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setOpen(false)}
              disabled={bootstrapMika.isPending}
            >
              {t(($) => $.mika_setup.cancel)}
            </Button>
            <Button
              onClick={handleStart}
              disabled={!runtimeId || bootstrapMika.isPending}
            >
              {bootstrapMika.isPending && (
                <Loader2 aria-hidden className="size-4 animate-spin" />
              )}
              {t(($) => $.mika_setup.action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function OrphanRuntimeProfiles({
  runtimes,
  now,
}: {
  runtimes: ReturnType<typeof pendingRuntimeFromProfile>[];
  now: number;
}) {
  const { t } = useT("runtimes");
  return (
    <section>
      <div className="mb-3">
        <h2 className="text-body font-semibold">
          {t(($) => $.profiles.unassigned_title)}
        </h2>
        <p className="mt-1 text-caption text-muted-foreground">
          {t(($) => $.profiles.unassigned_description)}
        </p>
      </div>
      <RuntimeList runtimes={runtimes} now={now} />
    </section>
  );
}

export default RuntimesPage;
