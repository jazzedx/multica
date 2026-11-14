// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { ApiClient } from "../api/client";
import { chatKeys, chatSessionTaskRunsOptions } from "./queries";

const mocks = vi.hoisted(() => ({ listChatSessionTaskRuns: vi.fn() }));
vi.mock("../api", () => ({ api: { listChatSessionTaskRuns: mocks.listChatSessionTaskRuns } }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetAllMocks();
});

describe("listChatSessionTaskRuns", () => {
  const respondWith = (body: unknown) => {
    const fetchMock = vi.fn().mockImplementation(async () => Response.json(body));
    vi.stubGlobal("fetch", fetchMock);
    return { client: new ApiClient("https://api.example.test"), fetchMock };
  };

  it("reads the session's runs with their usage", async () => {
    const { client, fetchMock } = respondWith([
      {
        id: "task-1",
        status: "completed",
        chat_session_id: "session-1",
        usage: [{ provider: "anthropic", model: "claude-opus-5", input_tokens: 1200, output_tokens: 300 }],
      },
      { id: "task-2", status: "failed", chat_session_id: "session-1" },
    ]);

    const runs = await client.listChatSessionTaskRuns("session-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/chat/sessions/session-1/task-runs");
    expect(runs.map((run) => run.id)).toEqual(["task-1", "task-2"]);
    expect(runs[0]?.usage?.[0]?.input_tokens).toBe(1200);
    expect(runs[1]?.usage).toBeUndefined();
  });

  // The chat itself must keep working when this history cannot be read.
  it("reads a malformed body as no runs", async () => {
    const { client } = respondWith({ runs: "not-a-list" });
    await expect(client.listChatSessionTaskRuns("session-1")).resolves.toEqual([]);
  });
});

describe("chat session run history freshness", () => {
  it("refetches when the session's pending task is invalidated, and only that session's", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    mocks.listChatSessionTaskRuns.mockResolvedValue([]);
    const invalidated = (sessionId: string) =>
      queryClient.getQueryState(chatKeys.taskRuns(sessionId))?.isInvalidated;
    await queryClient.fetchQuery(chatSessionTaskRunsOptions("session-1"));
    await queryClient.fetchQuery(chatSessionTaskRunsOptions("session-2"));
    expect(mocks.listChatSessionTaskRuns).toHaveBeenCalledTimes(2);

    // What the realtime layer does when a run of session-1 starts or ends.
    await queryClient.invalidateQueries({ queryKey: chatKeys.pendingTask("session-1") });

    expect(invalidated("session-1")).toBe(true);
    expect(invalidated("session-2")).toBe(false);
  });
});
