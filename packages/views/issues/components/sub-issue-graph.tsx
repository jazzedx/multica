"use client";

import { useMemo, useState } from "react";
import { ChevronRight, Hourglass, Maximize2, TriangleAlert } from "@multica/ui/icons";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import type { Issue } from "@multica/core/types";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueRouteRef, useWorkspacePaths } from "@multica/core/paths";
import { ActorAvatar } from "../../common/actor-avatar";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { layoutDependencyGraph } from "../sub-issue-graph-layout";
import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";
import { StatusIcon } from "./status-icon";

const SIZE = { nodeWidth: 196, nodeHeight: 56, gapX: 44, gapY: 12 };

/** Horizontal padding of the dialog around the canvas (`p-4` on both sides). */
const DIALOG_PADDING = 32;

/**
 * What the graph says about a node. `waiting` and `stale` are the ones a list
 * of statuses cannot show: `waiting` reads as unstarted there, and `stale` as
 * ordinary progress. A stale node has started or finished while something it
 * depends on is not done, typically because that was reopened.
 */
type NodeState = "done" | "cancelled" | "active" | "ready" | "waiting" | "stale";

interface GraphNode {
  id: string;
  identifier: string;
  title: string;
  status: string;
  order: number;
  /** A sub-issue of this parent, as opposed to an issue elsewhere that one of them waits for. */
  issue: Issue | null;
}

const NODE_CLASS: Record<NodeState, string> = {
  done: "bg-muted/40",
  cancelled: "bg-muted/40",
  active: "border-primary/60 bg-background shadow-sm",
  ready: "bg-background",
  waiting: "border-dashed bg-background",
  stale: "border-warning bg-background",
};

/**
 * An edge is met once its source is done and broken when the source was
 * cancelled: the wait ended, but what it waited for was never delivered. It
 * is stale when the source is not finished but the work at its far end went
 * ahead anyway.
 */
const EDGE_CLASS = {
  met: "text-faint-foreground",
  pending: "text-muted-foreground",
  broken: "text-destructive",
  stale: "text-warning",
} as const;

/**
 * A parent's sub-issues drawn as the graph their blockers form: what can run
 * now, what waits and for what. Edges come from each sub-issue's `blocked_by`,
 * stage waits included, so the graph says what the platform will hold.
 */
export function SubIssueGraph({ issues }: { issues: Issue[] }) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { colorOf, iconOf, categoryOf } = useIssueStatuses(wsId);
  const [open, setOpen] = useState(true);
  const [expanded, setExpanded] = useState(false);

  const graph = useMemo(() => {
    const nodes = new Map<string, GraphNode>();
    for (const issue of issues) {
      nodes.set(issue.id, {
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        status: issue.status,
        order: issue.number,
        issue,
      });
    }
    const links: { from: string; to: string }[] = [];
    const seen = new Set<string>();
    let external = 0;
    for (const issue of issues) {
      for (const blocker of issue.blocked_by ?? []) {
        const key = `${blocker.id}>${issue.id}`;
        if (seen.has(key)) continue;
        seen.add(key);
        if (!nodes.has(blocker.id)) {
          nodes.set(blocker.id, {
            id: blocker.id,
            identifier: blocker.identifier,
            title: blocker.title,
            status: blocker.status,
            // Ahead of the sub-issues in its column.
            order: --external - issues.length,
            issue: null,
          });
        }
        links.push({ from: blocker.id, to: issue.id });
      }
    }
    const list = [...nodes.values()];
    return { nodes, layout: layoutDependencyGraph(list, links, SIZE) };
  }, [issues]);

  if (graph.layout.edges.length === 0) return null;

  const isDone = (node: GraphNode) => categoryOf(node.status) === "done";
  const isCancelled = (node: GraphNode) => categoryOf(node.status) === "closed";
  const openBlockers = (id: string) =>
    graph.layout.edges
      .filter((edge) => edge.to === id)
      .map((edge) => graph.nodes.get(edge.from)!)
      .filter((blocker) => !isDone(blocker) && !isCancelled(blocker));
  /** Work went ahead here: the sub-issue is in progress or finished. */
  const wentAhead = (node: GraphNode) =>
    !!node.issue && (isDone(node) || categoryOf(node.status) === "started");
  const stateOf = (node: GraphNode): NodeState => {
    const blockers = openBlockers(node.id);
    if (wentAhead(node) && blockers.length > 0) return "stale";
    if (isDone(node)) return "done";
    if (isCancelled(node)) return "cancelled";
    if (categoryOf(node.status) === "started") return "active";
    return blockers.length === 0 ? "ready" : "waiting";
  };

  const canvas = (
    <div className="relative" style={{ width: graph.layout.width, height: graph.layout.height }}>
      <svg
        aria-hidden="true"
        className="pointer-events-none absolute inset-0"
        width={graph.layout.width}
        height={graph.layout.height}
      >
        {graph.layout.edges.map((edge) => {
          const source = graph.nodes.get(edge.from)!;
          const kind = isDone(source)
            ? "met"
            : isCancelled(source)
              ? "broken"
              : wentAhead(graph.nodes.get(edge.to)!)
                ? "stale"
                : "pending";
          return (
            <g key={`${edge.from}>${edge.to}`} data-edge={kind} className={EDGE_CLASS[kind]}>
              <path
                d={edge.path}
                fill="none"
                stroke="currentColor"
                strokeWidth={1.5}
                strokeDasharray={kind === "met" ? undefined : "4 3"}
              />
              <circle cx={edge.end.x} cy={edge.end.y} r={2.5} fill="currentColor" />
            </g>
          );
        })}
      </svg>
      <ul className="absolute inset-0 m-0 list-none p-0">
        {graph.layout.nodes.map((placed) => {
          const node = graph.nodes.get(placed.id)!;
          const state = stateOf(node);
          const held = state === "waiting";
          const blockerNames = openBlockers(node.id).map((blocker) => blocker.identifier).join(", ");
          const tooltip = held
            ? t(($) => $.dependencies.marker, { identifiers: blockerNames })
            : state === "stale"
              ? t(($) => $.dependencies.stale_marker, { identifiers: blockerNames })
              : node.issue
                ? undefined
                : t(($) => $.dependencies.graph_external);
          const finished = state === "done" || state === "cancelled" || (state === "stale" && isDone(node));
          return (
            <li
              key={node.id}
              data-state={state}
              className="absolute"
              style={{ left: placed.x, top: placed.y, width: SIZE.nodeWidth, height: SIZE.nodeHeight }}
            >
              <AppLink
                href={paths.issueDetail(issueRouteRef(node))}
                newTabTitle={node.identifier}
                title={tooltip}
                className={cn(
                  "flex h-full flex-col justify-center gap-0.5 rounded-md border px-2.5 text-caption transition-colors hover:bg-accent/50",
                  NODE_CLASS[state],
                  !node.issue && "border-dotted",
                )}
              >
                <span className="flex min-w-0 items-center gap-1.5">
                  <StatusIcon
                    status={node.status}
                    color={colorOf(node.status)}
                    icon={iconOf(node.status)}
                    category={categoryOf(node.status)}
                    className="h-3.5 w-3.5 shrink-0"
                  />
                  <span className="shrink-0 tabular-nums text-muted-foreground">{node.identifier}</span>
                  {held ? (
                    <Hourglass aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
                  ) : null}
                  {state === "stale" ? (
                    <TriangleAlert aria-hidden="true" className="size-3 shrink-0 text-warning" />
                  ) : null}
                  {node.issue ? (
                    <span className="ml-auto flex shrink-0 items-center gap-1">
                      <IssueAgentActivityIndicator issueId={node.id} hoverCard={false} />
                      {node.issue.assignee_type && node.issue.assignee_id ? (
                        <ActorAvatar
                          actorType={node.issue.assignee_type}
                          actorId={node.issue.assignee_id}
                          size="xs"
                          profileLink={false}
                        />
                      ) : null}
                    </span>
                  ) : null}
                </span>
                <span
                  className={cn(
                    "truncate",
                    finished ? "text-muted-foreground" : "text-foreground",
                    state === "cancelled" && "line-through",
                  )}
                >
                  {node.title}
                </span>
              </AppLink>
            </li>
          );
        })}
      </ul>
    </div>
  );

  return (
    <Collapsible
      render={<section aria-label={t(($) => $.dependencies.graph_title)} />}
      open={open}
      onOpenChange={setOpen}
      className="mb-2"
    >
      <div className="mb-1.5 flex items-center gap-1">
        <CollapsibleTrigger className="flex items-center gap-1.5 text-caption font-medium text-muted-foreground transition-colors hover:text-foreground">
          <ChevronRight
            aria-hidden="true"
            className={cn(
              "h-3 w-3 transition-transform duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none",
              open && "rotate-90",
            )}
          />
          {t(($) => $.dependencies.graph_title)}
        </CollapsibleTrigger>
        {/* The reading column is narrower than most graphs; this shows all of it. */}
        <Dialog open={expanded} onOpenChange={setExpanded}>
          <DialogTrigger
            render={
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={t(($) => $.dependencies.graph_expand)}
                title={t(($) => $.dependencies.graph_expand)}
                className="ml-auto text-muted-foreground"
              />
            }
          >
            <Maximize2 />
          </DialogTrigger>
          {/* As wide as the graph, up to the viewport. An explicit width:
              the popup is centred from `left: 50%`, which leaves a
              shrink-to-fit width only half the viewport to grow into. */}
          <DialogContent
            className="sm:max-w-none"
            style={{ width: `min(calc(100% - 2rem), ${graph.layout.width + DIALOG_PADDING}px)` }}
          >
            <DialogHeader>
              <DialogTitle>{t(($) => $.dependencies.graph_title)}</DialogTitle>
            </DialogHeader>
            {/* Following a node leaves the page the dialog belongs to. */}
            <div
              className="overflow-auto"
              onClick={(event) => {
                if ((event.target as HTMLElement).closest("a")) setExpanded(false);
              }}
            >
              {canvas}
            </div>
          </DialogContent>
        </Dialog>
      </div>
      <CollapsibleContent>
        <div className="overflow-x-auto rounded-lg border bg-card/30 p-3">
          {canvas}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
