"use client";

import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Hourglass, TriangleAlert, X } from "@multica/ui/icons";
import type { IssueDependencyRef } from "@multica/core/types";
import {
  isWaiting,
  issueDependenciesOptions,
  subIssueBlockersOptions,
  useRemoveIssueDependency,
} from "@multica/core/issues/dependencies";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { StatusIcon } from "./status-icon";
import { useT } from "../../i18n";

// Issue dependencies. An issue that waits for others starts no run until they
// are all done, then starts on its own, so the one thing a reader must be able
// to see is why an assigned issue is sitting still and what will move it.

function DependencyChip({
  dependency,
  onRemove,
  removeLabel,
}: {
  dependency: IssueDependencyRef;
  onRemove?: () => void;
  removeLabel?: string;
}) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { colorOf, iconOf, categoryOf } = useIssueStatuses(wsId);
  return (
    <span className="inline-flex min-w-0 max-w-full items-center rounded-md border bg-background">
      <AppLink
        href={paths.issueDetail(dependency.id)}
        newTabTitle={dependency.identifier}
        className="group flex min-w-0 items-center gap-1.5 px-1.5 py-0.5"
      >
        <StatusIcon
          status={dependency.status}
          color={colorOf(dependency.status)}
          icon={iconOf(dependency.status)}
          category={categoryOf(dependency.status)}
          className="h-3.5 w-3.5 shrink-0"
        />
        <span className="shrink-0 text-muted-foreground">{dependency.identifier}</span>
        <span className="max-w-[16rem] truncate underline-offset-4 group-hover:underline">
          {dependency.title}
        </span>
      </AppLink>
      {onRemove ? (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={removeLabel}
          title={removeLabel}
          className="mr-0.5 shrink-0 text-muted-foreground"
          onClick={onRemove}
        >
          <X />
        </Button>
      ) : null}
    </span>
  );
}

/**
 * Strip above an issue's title: what it waits for and what waits for it.
 *
 * Self-contained on purpose: it owns its query and renders nothing for an
 * issue without dependencies, so the page hosting it needs one line.
 */
export function IssueDependencyStrip({
  issueId,
  status,
}: {
  issueId: string;
  /** The issue's own status, when the host has it: it tells waiting from having gone ahead. */
  status?: string;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { categoryOf } = useIssueStatuses(wsId);
  const { data } = useQuery(issueDependenciesOptions(wsId, issueId));
  const remove = useRemoveIssueDependency(wsId, issueId);

  const blockedBy = data?.blocked_by ?? [];
  const blocks = data?.blocks ?? [];
  if (blockedBy.length === 0 && blocks.length === 0) return null;

  const unmet = isWaiting(data);
  // An issue that has started or finished is not waiting for anything. If what
  // it depends on is not done, that was reopened, or the issue was started
  // early: either way it stands on an input that may have changed.
  const ownCategory = status === undefined ? undefined : categoryOf(status);
  const stale = unmet && (ownCategory === "started" || ownCategory === "done");
  const waiting = unmet && !stale;
  // A cancelled issue (the closed category) never becomes done, so waiting
  // for one never ends.
  const stuck = blockedBy.some(
    (blocker) => !blocker.done && categoryOf(blocker.status) === "closed",
  );

  return (
    <section
      aria-label={t(($) => $.dependencies.title)}
      className="mb-4 space-y-1.5 rounded-md border bg-muted/40 px-3 py-2 text-caption"
    >
      {blockedBy.length > 0 ? (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className={`flex shrink-0 items-center gap-1 ${stale ? "text-warning" : "text-muted-foreground"}`}>
            {stale ? (
              <TriangleAlert aria-hidden="true" className="size-3.5" />
            ) : (
              <Hourglass aria-hidden="true" className="size-3.5" />
            )}
            {waiting ? t(($) => $.dependencies.waiting_for) : t(($) => $.dependencies.waited_for)}
          </span>
          {blockedBy.map((blocker) => {
            const label = t(($) => $.dependencies.remove, { identifier: blocker.identifier });
            return (
              <DependencyChip
                key={blocker.id}
                dependency={blocker}
                removeLabel={label}
                onRemove={() =>
                  remove.mutate(blocker.id, {
                    onError: () => toast.error(t(($) => $.dependencies.remove_failed)),
                  })
                }
              />
            );
          })}
          {waiting ? (
            <span className="text-muted-foreground">
              {stuck
                ? t(($) => $.dependencies.cancelled_hint)
                : t(($) => $.dependencies.starts_when_done)}
            </span>
          ) : null}
          {stale ? <span className="text-muted-foreground">{t(($) => $.dependencies.stale_hint)}</span> : null}
        </div>
      ) : null}
      {blocks.length > 0 ? (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="shrink-0 text-muted-foreground">{t(($) => $.dependencies.blocks)}</span>
          {blocks.map((dependent) => (
            <DependencyChip key={dependent.id} dependency={dependent} />
          ))}
        </div>
      ) : null}
    </section>
  );
}

/** How many issues a waiting marker names before it counts the rest. */
const MARKER_LIMIT = 2;

/**
 * "Waiting for MUL-12" beside a sub-issue that is being held, or a warning
 * beside one that went ahead while what it depends on is not done. Every row
 * of a parent shares one request, and a row with nothing unmet renders
 * nothing. Plain text, not a link: the row it sits in is already one.
 */
export function SubIssueWaitingMarker({
  parentId,
  issueId,
  status,
}: {
  parentId: string | null | undefined;
  issueId: string;
  /** The sub-issue's own status, which tells waiting from having gone ahead. */
  status?: string;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { categoryOf } = useIssueStatuses(wsId);
  const { data } = useQuery({
    ...subIssueBlockersOptions(wsId, parentId ?? ""),
    enabled: Boolean(parentId),
  });
  const open = (data?.edges ?? [])
    .filter((edge) => edge.issue_id === issueId && !edge.blocked_by.done)
    .map((edge) => edge.blocked_by);
  if (open.length === 0) return null;

  const named = open.slice(0, MARKER_LIMIT).map((blocker) => blocker.identifier).join(", ");
  const rest = open.length - MARKER_LIMIT;
  const identifiers = rest > 0 ? `${named} +${rest}` : named;
  const tooltip = open.map((blocker) => `${blocker.identifier} ${blocker.title}`).join("\n");
  const ownCategory = status === undefined ? undefined : categoryOf(status);
  if (ownCategory === "closed") return null;
  const stale = ownCategory === "started" || ownCategory === "done";
  return (
    <span
      title={tooltip}
      className={`inline-flex shrink-0 items-center gap-1 rounded-full bg-muted/60 px-1.5 py-0.5 text-micro ${stale ? "text-warning" : "text-muted-foreground"}`}
    >
      {stale ? (
        <TriangleAlert aria-hidden="true" className="size-3" />
      ) : (
        <Hourglass aria-hidden="true" className="size-3" />
      )}
      {stale
        ? t(($) => $.dependencies.stale_marker, { identifiers })
        : t(($) => $.dependencies.marker, { identifiers })}
    </span>
  );
}
