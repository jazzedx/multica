import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentTask } from "@multica/core/types/agent";
import { renderWithI18n } from "../../test/i18n";
import { ChatRunTranscriptButton } from "./chat-run-transcript";

const mocks = vi.hoisted(() => ({ listChatSessionTaskRuns: vi.fn() }));

vi.mock("@multica/core/api", () => ({
  api: {
    listChatSessionTaskRuns: mocks.listChatSessionTaskRuns,
    listAgents: async () => [{ id: "agent-1", name: "Mika" }],
  },
}));
// The dialog has its own tests; here it only matters which run is handed to it.
vi.mock("../../common/task-transcript", () => ({
  TranscriptButton: ({ task, agentName, title }: { task: AgentTask; agentName: string; title: string }) => (
    <button type="button" data-task={task.id} data-agent={agentName} data-usage-rows={task.usage?.length ?? 0}>
      {title}
    </button>
  ),
}));

function run(id: string, overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id,
    agent_id: "agent-1",
    workspace_id: "ws-1",
    status: "completed",
    chat_session_id: "session-1",
    ...overrides,
  } as AgentTask;
}

function renderButton(taskId: string | null) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <ChatRunTranscriptButton sessionId="session-1" taskId={taskId} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listChatSessionTaskRuns.mockResolvedValue([
    run("task-2"),
    run("task-1", {
      usage: [
        { provider: "anthropic", model: "claude-opus-5", input_tokens: 1200, output_tokens: 300 },
      ] as AgentTask["usage"],
    }),
  ]);
});

describe("ChatRunTranscriptButton", () => {
  it("opens the transcript of the run behind the reply, with its usage", async () => {
    renderButton("task-1");

    const button = await screen.findByRole("button", { name: "View transcript" });
    expect(button).toHaveAttribute("data-task", "task-1");
    expect(button).toHaveAttribute("data-usage-rows", "1");
    await waitFor(() => expect(button).toHaveAttribute("data-agent", "Mika"));
    expect(mocks.listChatSessionTaskRuns).toHaveBeenCalledWith("session-1");
  });

  it("offers nothing for a reply whose run is not in the session's history", async () => {
    renderButton("task-gone");

    await waitFor(() => expect(mocks.listChatSessionTaskRuns).toHaveBeenCalled());
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("stops offering the transcript once the server refuses the chat", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderWithI18n(
      <QueryClientProvider client={client}>
        <ChatRunTranscriptButton sessionId="session-1" taskId="task-1" />
      </QueryClientProvider>,
    );
    await screen.findByRole("button", { name: "View transcript" });

    mocks.listChatSessionTaskRuns.mockRejectedValue(new Error("you do not have access to this agent"));
    await client.invalidateQueries();

    await waitFor(() => expect(screen.queryByRole("button")).not.toBeInTheDocument());
  });

  it("does not load the history for a reply that no run produced", () => {
    renderButton(null);

    expect(mocks.listChatSessionTaskRuns).not.toHaveBeenCalled();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
