import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentTask } from "@multica/core/types/agent";
import { renderWithI18n } from "../../test/i18n";
import { ChatSessionUsage } from "./chat-session-usage";

const mocks = vi.hoisted(() => ({ listChatSessionTaskRuns: vi.fn() }));

vi.mock("@multica/core/api", () => ({
  api: { listChatSessionTaskRuns: mocks.listChatSessionTaskRuns },
}));

function run(id: string, overrides: Partial<AgentTask> = {}): AgentTask {
  return { id, agent_id: "agent-1", status: "completed", chat_session_id: "session-1", ...overrides } as AgentTask;
}

const usage = (input: number, output: number) =>
  [
    {
      provider: "some-provider",
      model: "a-model-with-no-known-rate",
      input_tokens: input,
      output_tokens: output,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
    },
  ] as AgentTask["usage"];

function renderUsage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <ChatSessionUsage sessionId="session-1" />
    </QueryClientProvider>,
  );
}

beforeEach(() => vi.clearAllMocks());

describe("ChatSessionUsage", () => {
  it("totals the chat's runs and breaks the figure down on demand", async () => {
    mocks.listChatSessionTaskRuns.mockResolvedValue([
      run("task-3", {
        started_at: "2026-11-14T10:00:00Z",
        completed_at: "2026-11-14T10:01:30Z",
        usage: usage(1000, 200),
      }),
      run("task-2", {
        started_at: "2026-11-14T09:00:00Z",
        completed_at: "2026-11-14T09:00:30Z",
        usage: usage(2000, 300),
      }),
      // Queued behind the others: no timing, no usage yet.
      run("task-1", { status: "queued" }),
    ]);
    const user = userEvent.setup();
    renderUsage();

    const chip = await screen.findByRole("button", { name: "Usage of this chat" });
    expect(chip).toHaveTextContent("3.5K");

    await user.click(chip);
    const details = await screen.findByRole("dialog");
    const row = (label: string) => within(details).getByText(label).parentElement;
    expect(row("Runs")).toHaveTextContent("3");
    expect(row("Run time")).toHaveTextContent("2m");
    expect(row("Input")).toHaveTextContent("3K");
    expect(row("Output")).toHaveTextContent("500");
    expect(within(details).queryByText("Cache read")).not.toBeInTheDocument();
  });

  it("stops showing the figure once the server refuses the chat", async () => {
    mocks.listChatSessionTaskRuns.mockResolvedValueOnce([run("task-1", { usage: usage(1000, 200) })]);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderWithI18n(
      <QueryClientProvider client={client}>
        <ChatSessionUsage sessionId="session-1" />
      </QueryClientProvider>,
    );
    await screen.findByRole("button", { name: "Usage of this chat" });

    // The chat's agent was made private to someone else; the next refresh is refused.
    mocks.listChatSessionTaskRuns.mockRejectedValue(new Error("you do not have access to this agent"));
    await client.invalidateQueries();

    await waitFor(() => expect(screen.queryByRole("button")).not.toBeInTheDocument());
  });

  // An empty figure would claim the chat cost nothing.
  it("shows nothing while no run has reported usage", async () => {
    mocks.listChatSessionTaskRuns.mockResolvedValue([run("task-1"), run("task-2", { status: "failed" })]);
    renderUsage();

    await waitFor(() => expect(mocks.listChatSessionTaskRuns).toHaveBeenCalledWith("session-1"));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
