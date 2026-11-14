import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { DashboardUsageBySource } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { UsageBySource } from "./usage-by-source";

const mocks = vi.hoisted(() => ({ getDashboardUsageBySource: vi.fn(), listAgents: vi.fn() }));

vi.mock("@multica/core/api", () => ({
  api: { getDashboardUsageBySource: mocks.getDashboardUsageBySource, listAgents: mocks.listAgents },
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));

// A model no rate table knows, so a row's cost is exactly what it reports.
function row(source: string, agentId: string, tokens: number, dollars: number, runs: number): DashboardUsageBySource {
  return {
    source,
    agent_id: agentId,
    provider: "some-provider",
    model: "a-model-with-no-known-rate",
    input_tokens: tokens,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    cost_usd_ticks: dollars * 1e10,
    task_count: runs,
  };
}

function renderCard(locale: "en" | "zh-Hans" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <UsageBySource days={7} projectId="project-1" tz="Asia/Shanghai" />
    </QueryClientProvider>,
    { locale },
  );
}

/** The text of each row in a list, without the rows of lists nested in it. */
function rowTexts(list: HTMLElement): string[] {
  return within(list)
    .getAllByRole("listitem")
    .filter((item) => item.parentElement === list)
    .map((item) => item.textContent ?? "");
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listAgents.mockResolvedValue([
    { id: "ada", name: "Ada" },
    { id: "turing", name: "Turing" },
  ]);
});

describe("UsageBySource", () => {
  it("lists each source with the agents behind it, largest first", async () => {
    mocks.getDashboardUsageBySource.mockResolvedValue([
      row("chat", "ada", 2_000, 1, 4),
      row("issue", "turing", 6_000, 2, 5),
      row("issue", "ada", 3_000, 4, 7),
      row("issue", "__restricted_agents__", 500, 1, 1),
      row("issue", "an-agent-that-was-deleted", 500, 1, 1),
      row("some-future-source", "ada", 10, 0, 1),
    ]);
    renderCard();

    const card = await screen.findByRole("region", { name: "By source" });
    expect(within(card).getAllByRole("button", { expanded: true }).map((b) => b.textContent)).toEqual([
      "Issues4 agents",
      "Chat1 agents",
      "some-future-source1 agents",
    ]);
    expect(rowTexts(within(card).getByRole("list", { name: "Issues" }))).toEqual([
      // Shares here are of Issues, which is 89% of the window itself.
      "Ada50%3K$4.007",
      "Turing25%6K$2.005",
      // A tie on share and tokens: the two synthetic buckets, by id.
      "Deleted agents13%500$1.001",
      "Other agents13%500$1.001",
    ]);
    expect(rowTexts(within(card).getByRole("list", { name: "Chat" }))).toEqual(["Ada100%2K$1.004"]);
    expect(mocks.getDashboardUsageBySource).toHaveBeenCalledWith({
      days: 7,
      project_id: "project-1",
      tz: "Asia/Shanghai",
    });
  });

  it("collapses a source, and shows only its largest agents until asked for all", async () => {
    mocks.listAgents.mockResolvedValue(
      Array.from({ length: 7 }, (_, i) => ({ id: `agent-${i}`, name: `Agent ${i}` })),
    );
    mocks.getDashboardUsageBySource.mockResolvedValue(
      Array.from({ length: 7 }, (_, i) => row("chat", `agent-${i}`, 100, 7 - i, 1)),
    );
    const user = userEvent.setup();
    renderCard();

    const card = await screen.findByRole("region", { name: "By source" });
    const agents = () => rowTexts(within(card).getByRole("list", { name: "Chat" }));
    expect(agents()).toHaveLength(5);
    expect(agents()[0]).toContain("Agent 0");

    await user.click(within(card).getByRole("button", { name: "Show all" }));
    expect(agents()).toHaveLength(7);
    await user.click(within(card).getByRole("button", { name: "Show top 5" }));
    expect(agents()).toHaveLength(5);

    await user.click(within(card).getByRole("button", { name: /^Chat/ }));
    expect(within(card).queryByRole("list", { name: "Chat" })).not.toBeInTheDocument();
    expect(within(card).getByRole("button", { name: /^Chat/ })).toHaveAttribute("aria-expanded", "false");
  });

  it("names the sources in the UI language", async () => {
    mocks.getDashboardUsageBySource.mockResolvedValue([
      row("chat", "ada", 100, 1, 1),
      row("issue", "ada", 100, 3, 1),
    ]);
    renderCard("zh-Hans");

    const card = await screen.findByRole("region", { name: "按来源" });
    expect(within(card).getByRole("list", { name: "任务" })).toBeInTheDocument();
    expect(within(card).getByRole("list", { name: "聊天" })).toBeInTheDocument();
  });

  // The page shows its own empty state; an empty card would add nothing.
  it("renders nothing for a window with no usage", async () => {
    mocks.getDashboardUsageBySource.mockResolvedValue([]);
    renderCard();

    await waitFor(() => expect(mocks.getDashboardUsageBySource).toHaveBeenCalled());
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
  });
});
