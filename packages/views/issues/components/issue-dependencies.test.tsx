import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDependencyRef } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { IssueDependencyStrip, SubIssueWaitingMarker } from "./issue-dependencies";

const mocks = vi.hoisted(() => ({
  listIssueDependencies: vi.fn(),
  removeIssueDependency: vi.fn(),
  listSubIssueBlockers: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: mocks }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/ws/issues/${id}` }),
}));
vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    colorOf: () => undefined,
    iconOf: () => undefined,
    categoryOf: (status: string) => (status === "cancelled" ? "closed" : status === "done" ? "done" : "unstarted"),
  }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock("./status-icon", () => ({ StatusIcon: () => null }));

const ref = (n: number, status: string, title = `Issue ${n}`): IssueDependencyRef => ({
  id: `issue-${n}`,
  identifier: `MUL-${n}`,
  title,
  status,
  done: status === "done",
});

function renderInClient(ui: React.ReactElement, locale: "en" | "zh-Hans" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={client}>{ui}</QueryClientProvider>, { locale });
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("IssueDependencyStrip", () => {
  it("says what the issue waits for and that it starts by itself", async () => {
    mocks.listIssueDependencies.mockResolvedValue({
      blocked_by: [ref(1, "done", "Design the contract"), ref(2, "in_progress", "Implement the API")],
      blocks: [ref(4, "todo", "Integration test")],
    });
    renderInClient(<IssueDependencyStrip issueId="issue-3" />);

    const strip = await screen.findByRole("region", { name: "Dependencies" });
    expect(strip).toHaveTextContent("Waiting for");
    expect(strip).toHaveTextContent("Starts on its own when they are done");
    expect(within(strip).getByRole("link", { name: /MUL-2\s*Implement the API/ })).toHaveAttribute(
      "href",
      "/ws/issues/issue-2",
    );
    expect(strip).toHaveTextContent("Unblocks");
    expect(within(strip).getByRole("link", { name: /MUL-4\s*Integration test/ })).toBeInTheDocument();
    // Only what this issue waits for can be removed from here.
    expect(within(strip).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
      "Stop waiting for MUL-1",
      "Stop waiting for MUL-2",
    ]);
  });

  it("stops promising a start once everything it waited for is done", async () => {
    mocks.listIssueDependencies.mockResolvedValue({ blocked_by: [ref(1, "done")], blocks: [] });
    renderInClient(<IssueDependencyStrip issueId="issue-3" />);

    const strip = await screen.findByRole("region", { name: "Dependencies" });
    expect(strip).toHaveTextContent("Waited for");
    expect(strip).not.toHaveTextContent("Starts on its own");
  });

  it("explains that a cancelled issue will never release this one", async () => {
    mocks.listIssueDependencies.mockResolvedValue({ blocked_by: [ref(1, "cancelled")], blocks: [] });
    renderInClient(<IssueDependencyStrip issueId="issue-3" />, "zh-Hans");

    const strip = await screen.findByRole("region", { name: "依赖" });
    expect(strip).toHaveTextContent("已取消的任务不会完成，移除后才会开始。");
    expect(strip).not.toHaveTextContent("全部完成后自动开始");
  });

  it("removes a dependency and shows what the server says is left", async () => {
    mocks.listIssueDependencies.mockResolvedValue({ blocked_by: [ref(1, "cancelled"), ref(2, "todo")], blocks: [] });
    mocks.removeIssueDependency.mockResolvedValue({ blocked_by: [ref(2, "todo")], blocks: [] });
    renderInClient(<IssueDependencyStrip issueId="issue-3" />);

    await userEvent.click(await screen.findByRole("button", { name: "Stop waiting for MUL-1" }));

    await waitFor(() => expect(screen.queryByRole("button", { name: "Stop waiting for MUL-1" })).not.toBeInTheDocument());
    expect(mocks.removeIssueDependency).toHaveBeenCalledWith("issue-3", "issue-1");
    expect(screen.getByRole("button", { name: "Stop waiting for MUL-2" })).toBeInTheDocument();
  });

  it("renders nothing for an issue without dependencies", async () => {
    mocks.listIssueDependencies.mockResolvedValue({ blocked_by: [], blocks: [] });
    const { container } = renderInClient(<IssueDependencyStrip issueId="issue-3" />);
    await waitFor(() => expect(mocks.listIssueDependencies).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});

describe("SubIssueWaitingMarker", () => {
  it("names the open issues a sub-issue waits for and counts the rest", async () => {
    mocks.listSubIssueBlockers.mockResolvedValue({
      edges: [
        { issue_id: "issue-9", blocked_by: ref(1, "done") },
        { issue_id: "issue-9", blocked_by: ref(2, "todo") },
        { issue_id: "issue-9", blocked_by: ref(3, "in_progress") },
        { issue_id: "issue-9", blocked_by: ref(4, "todo") },
        { issue_id: "issue-8", blocked_by: ref(5, "todo") },
      ],
    });
    renderInClient(<SubIssueWaitingMarker parentId="parent" issueId="issue-9" />);

    expect(await screen.findByText("Waiting for MUL-2, MUL-3 +1")).toBeInTheDocument();
    expect(mocks.listSubIssueBlockers).toHaveBeenCalledWith("parent");
  });

  it("renders nothing for a sub-issue that is not waiting", async () => {
    mocks.listSubIssueBlockers.mockResolvedValue({ edges: [{ issue_id: "issue-9", blocked_by: ref(1, "done") }] });
    const { container } = renderInClient(<SubIssueWaitingMarker parentId="parent" issueId="issue-9" />);
    await waitFor(() => expect(mocks.listSubIssueBlockers).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("asks for nothing without a parent", () => {
    const { container } = renderInClient(<SubIssueWaitingMarker parentId={null} issueId="issue-9" />);
    expect(mocks.listSubIssueBlockers).not.toHaveBeenCalled();
    expect(container).toBeEmptyDOMElement();
  });
});
