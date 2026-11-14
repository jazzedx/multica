import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDeliveryOverview } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { DeliveryOverview } from "./delivery-overview";

const mocks = vi.hoisted(() => ({ getIssueDeliveryOverview: vi.fn() }));

vi.mock("@multica/core/api", () => ({ api: mocks }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

const overview = (patch: Partial<IssueDeliveryOverview> = {}): IssueDeliveryOverview => ({
  sub_issues: { total: 4, done: 3, cancelled: 0 },
  runs: { total: 17, failed: 0, active: 0 },
  rework_rounds: 2,
  human_actions: 1,
  pull_requests_merged: 4,
  started_at: "2026-01-01T10:00:00Z",
  finished_at: "2026-01-01T10:34:10Z",
  ...patch,
});

function renderOverview(revision = "a", locale: "en" | "zh-Hans" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <DeliveryOverview issueId="issue-1" revision={revision} />
    </QueryClientProvider>,
    { locale },
  );
}

/** Each figure's value by its label. */
function figures(region: HTMLElement): Record<string, string> {
  return Object.fromEntries(
    within(region)
      .getAllByRole("term")
      .map((term) => [term.textContent ?? "", term.nextElementSibling?.textContent ?? ""]),
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("DeliveryOverview", () => {
  it("says what a finished delivery took", async () => {
    mocks.getIssueDeliveryOverview.mockResolvedValue(overview());
    renderOverview();

    const region = await screen.findByRole("region", { name: "Delivery overview" });
    expect(figures(region)).toEqual({
      "Sub-issues": "3/4",
      Runs: "17",
      Rework: "2",
      "Human actions": "1",
      "Merged PRs": "4",
      Took: "34 min",
    });
  });

  it("shows failed runs, leaves out pull requests when there are none, and keeps counting while open", async () => {
    mocks.getIssueDeliveryOverview.mockResolvedValue(
      overview({ runs: { total: 5, failed: 2, active: 1 }, pull_requests_merged: 0, finished_at: null, started_at: new Date(Date.now() - 125 * 60_000).toISOString() }),
    );
    renderOverview("a", "zh-Hans");

    const region = await screen.findByRole("region", { name: "交付概览" });
    const shown = figures(region);
    expect(shown["运行"]).toBe("52 次失败");
    expect(shown["合并的 PR"]).toBeUndefined();
    expect(shown["已进行"]).toBe("2 小时 5 分钟");
  });

  it("renders nothing for an issue without sub-issues", async () => {
    mocks.getIssueDeliveryOverview.mockResolvedValue(overview({ sub_issues: { total: 0, done: 0, cancelled: 0 } }));
    const { container } = renderOverview();
    await waitFor(() => expect(mocks.getIssueDeliveryOverview).toHaveBeenCalledWith("issue-1"));
    expect(container).toBeEmptyDOMElement();
  });
});
