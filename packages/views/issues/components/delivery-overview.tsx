"use client";

import { useQuery } from "@tanstack/react-query";
import { issueDeliveryOverviewOptions, leadTime, type LeadTime } from "@multica/core/issues/delivery";
import { useWorkspaceId } from "@multica/core/hooks";
import { useT } from "../../i18n";

function Figure({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div title={hint}>
      <dt className="text-caption text-muted-foreground">{label}</dt>
      <dd className="m-0 text-body font-medium tabular-nums">{children}</dd>
    </div>
  );
}

/**
 * What it took to deliver a parent issue so far: how far its sub-issues are,
 * how often agents ran, how often work went back, how often a person stepped
 * in, and how long it has taken. Token use and run time are left to the
 * execution log, which already sums them for a sub-tree.
 *
 * Self-contained on purpose: it owns its query and renders nothing for an
 * issue without sub-issues. `revision` is anything the host holds that
 * changes when the work moves on, so the figures follow without an event of
 * their own.
 */
export function DeliveryOverview({ issueId, revision }: { issueId: string; revision?: string }) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { data, dataUpdatedAt } = useQuery(issueDeliveryOverviewOptions(wsId, issueId, revision));
  if (!data || data.sub_issues.total === 0) return null;

  const wordLeadTime = (time: LeadTime | null): string => {
    if (!time) return "–";
    switch (time.unit) {
      case "under_minute":
        return t(($) => $.delivery_overview.under_minute);
      case "minutes":
        return t(($) => $.delivery_overview.minutes, { minutes: time.minutes });
      case "hours":
        return t(($) => $.delivery_overview.hours, { hours: time.hours, minutes: time.minutes });
      case "days":
        return t(($) => $.delivery_overview.days, { days: time.days, hours: time.hours });
    }
  };
  // Measured to when the figures were read, not to a clock of its own: it
  // moves with every refresh and never claims more precision than that.
  const elapsed = leadTime(data.started_at, data.finished_at, dataUpdatedAt);

  return (
    <section aria-label={t(($) => $.delivery_overview.title)} className="mb-2">
      <dl className="m-0 flex flex-wrap gap-x-6 gap-y-2 rounded-lg border bg-card/30 px-3 py-2">
        <Figure
          label={t(($) => $.delivery_overview.sub_issues)}
          hint={data.sub_issues.cancelled > 0 ? t(($) => $.delivery_overview.cancelled, { count: data.sub_issues.cancelled }) : undefined}
        >
          {data.sub_issues.done}/{data.sub_issues.total}
        </Figure>
        <Figure label={t(($) => $.delivery_overview.runs)}>
          {data.runs.total}
          {data.runs.failed > 0 ? (
            <span className="ml-1.5 text-caption font-normal text-destructive">
              {t(($) => $.delivery_overview.failed, { count: data.runs.failed })}
            </span>
          ) : null}
        </Figure>
        <Figure label={t(($) => $.delivery_overview.rework)} hint={t(($) => $.delivery_overview.rework_hint)}>
          {data.rework_rounds}
        </Figure>
        <Figure label={t(($) => $.delivery_overview.human)} hint={t(($) => $.delivery_overview.human_hint)}>
          {data.human_actions}
        </Figure>
        {data.pull_requests_merged > 0 ? (
          <Figure label={t(($) => $.delivery_overview.merged)}>{data.pull_requests_merged}</Figure>
        ) : null}
        <Figure label={data.finished_at ? t(($) => $.delivery_overview.took) : t(($) => $.delivery_overview.running_for)}>
          {wordLeadTime(elapsed)}
        </Figure>
      </dl>
    </section>
  );
}
