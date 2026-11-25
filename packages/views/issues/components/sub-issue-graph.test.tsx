import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Issue, IssueBlocker } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { SubIssueGraph } from "./sub-issue-graph";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/ws/issues/${id}` }),
}));
vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    colorOf: () => undefined,
    iconOf: () => undefined,
    categoryOf: (status: string) =>
      status === "done" ? "done" : status === "cancelled" ? "closed" : status === "in_progress" ? "started" : "unstarted",
  }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, title, children }: { href: string; title?: string; children: React.ReactNode }) => (
    <a href={href} title={title}>
      {children}
    </a>
  ),
}));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./issue-agent-activity-indicator", () => ({ IssueAgentActivityIndicator: () => null }));
vi.mock("./status-icon", () => ({ StatusIcon: () => null }));

const issue = (n: number, status: string, title: string, blockedBy: IssueBlocker[] = []): Issue =>
  ({
    id: `issue-${n}`,
    number: n,
    identifier: `MUL-${n}`,
    title,
    status,
    assignee_type: null,
    assignee_id: null,
    parent_issue_id: "parent",
    blocked_by: blockedBy,
  }) as unknown as Issue;

const ref = (n: number, status: string, title = `Issue ${n}`, via = "manual"): IssueBlocker => ({
  id: `issue-${n}`,
  identifier: `MUL-${n}`,
  title,
  status: status as IssueBlocker["status"],
  via,
  closed: status === "done" || status === "cancelled",
});

function renderGraph(issues: Issue[], locale: "en" | "zh-Hans" = "en") {
  return renderWithI18n(<SubIssueGraph issues={issues} />, { locale });
}

/** Each node's state by identifier, as the graph marks it. */
function states(graph: HTMLElement): Record<string, string | null> {
  return Object.fromEntries(
    within(graph)
      .getAllByRole("listitem")
      .map((item) => [item.textContent?.match(/MUL-\d+/)?.[0] ?? "", item.getAttribute("data-state")]),
  );
}

describe("SubIssueGraph", () => {
  it("shows what is done, running, ready and waiting, and for which issue", async () => {
    renderGraph([
      issue(1, "done", "Design"),
      issue(2, "in_progress", "API", [ref(1, "done")]),
      issue(3, "todo", "UI", [ref(1, "done")]),
      issue(4, "todo", "Integration test", [ref(2, "in_progress"), ref(3, "todo")]),
      issue(5, "todo", "Docs"),
    ]);

    const graph = await screen.findByRole("region", { name: "Dependency graph" });
    expect(states(graph)).toEqual({
      "MUL-1": "done",
      "MUL-2": "active",
      "MUL-3": "ready",
      "MUL-4": "waiting",
      "MUL-5": "ready",
    });
    const waiting = within(graph).getByRole("link", { name: /MUL-4\s*Integration test/ });
    expect(waiting).toHaveAttribute("title", "Waiting for MUL-2, MUL-3");
    expect(waiting).toHaveAttribute("href", "/ws/issues/MUL-4");
    // Two edges out of the done design are met; the two into the test are not.
    expect(graph.querySelectorAll('[data-edge="met"]')).toHaveLength(2);
    expect(graph.querySelectorAll('[data-edge="pending"]')).toHaveLength(2);
  });

  // A blocker's ref can lag behind the sub-issue the host passes, which is live.
  it("trusts the live sub-issue status over a stale blocker ref", async () => {
    renderGraph([issue(1, "done", "Design"), issue(2, "todo", "API", [ref(1, "todo")])]);

    const graph = await screen.findByRole("region", { name: "Dependency graph" });
    expect(states(graph)).toEqual({ "MUL-1": "done", "MUL-2": "ready" });
  });

  // A cancelled blocker ends the wait, but what it was to deliver never came.
  it("marks the edge from a cancelled issue as broken and the waiter as ready", async () => {
    renderGraph([issue(1, "cancelled", "Design"), issue(2, "todo", "API", [ref(1, "cancelled")])]);

    const graph = await screen.findByRole("region", { name: "Dependency graph" });
    expect(states(graph)).toEqual({ "MUL-1": "cancelled", "MUL-2": "ready" });
    expect(graph.querySelectorAll('[data-edge="broken"]')).toHaveLength(1);
  });

  it("draws stage waits like any other blocker", async () => {
    renderGraph([issue(1, "todo", "Research"), issue(2, "backlog", "Build", [ref(1, "todo", "Research", "stage")])]);

    const graph = await screen.findByRole("region", { name: "Dependency graph" });
    expect(states(graph)).toEqual({ "MUL-1": "ready", "MUL-2": "waiting" });
  });

  it("draws an issue from elsewhere that a sub-issue waits for", async () => {
    renderGraph([issue(2, "todo", "API", [ref(90, "todo", "Shared library")])], "zh-Hans");

    const graph = await screen.findByRole("region", { name: "依赖图" });
    expect(within(graph).getByRole("link", { name: /MUL-90\s*Shared library/ })).toHaveAttribute(
      "title",
      "不是本任务的子任务",
    );
    expect(within(graph).getByRole("link", { name: /MUL-2\s*API/ })).toHaveAttribute("title", "等待 MUL-90");
  });

  it("opens the whole graph in a dialog, and closes it when a node is followed", async () => {
    renderGraph([issue(1, "todo", "Design"), issue(2, "todo", "API", [ref(1, "todo")])]);

    await userEvent.click(await screen.findByRole("button", { name: "Expand" }));

    const dialog = await screen.findByRole("dialog", { name: "Dependency graph" });
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(2);
    const node = within(dialog).getByRole("link", { name: /MUL-2\s*API/ });
    node.addEventListener("click", (event) => event.preventDefault());
    await userEvent.click(node);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("can be folded away", async () => {
    renderGraph([issue(1, "todo", "Design"), issue(2, "todo", "API", [ref(1, "todo")])]);

    const toggle = await screen.findByRole("button", { name: "Dependency graph", expanded: true });
    await userEvent.click(toggle);

    expect(screen.getByRole("button", { name: "Dependency graph", expanded: false })).toBeInTheDocument();
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("renders nothing while no sub-issue waits for anything", () => {
    const { container } = renderGraph([issue(1, "todo", "Design"), issue(2, "todo", "API")]);
    expect(container).toBeEmptyDOMElement();
  });
});
