// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";
import { isWaiting } from "./dependencies";

afterEach(() => {
  vi.unstubAllGlobals();
});

const respondWith = (body: unknown) => {
  const fetchMock = vi.fn().mockImplementation(async () => Response.json(body));
  vi.stubGlobal("fetch", fetchMock);
  return { client: new ApiClient("https://api.example.test"), fetchMock };
};

const ref = (id: string, done: boolean) => ({
  id,
  identifier: `MUL-${id}`,
  title: `Issue ${id}`,
  status: done ? "done" : "todo",
  done,
});

describe("issue dependencies client", () => {
  it("reads both directions of an issue's dependencies", async () => {
    const { client, fetchMock } = respondWith({ blocked_by: [ref("1", true)], blocks: [ref("3", false)] });

    const dependencies = await client.listIssueDependencies("issue-2");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/issues/issue-2/dependencies");
    expect(dependencies.blocked_by.map((b) => b.identifier)).toEqual(["MUL-1"]);
    expect(dependencies.blocks.map((b) => b.identifier)).toEqual(["MUL-3"]);
  });

  it("reads a malformed body as no dependencies", async () => {
    const { client } = respondWith({ blocked_by: "nope" });
    await expect(client.listIssueDependencies("issue-2")).resolves.toEqual({ blocked_by: [], blocks: [] });
  });

  // An older server that omits the flag must not make a waiting issue look released.
  it("reads a blocker without the done flag as still open", async () => {
    const { client } = respondWith({ blocked_by: [{ id: "1", identifier: "MUL-1" }] });
    const dependencies = await client.listIssueDependencies("issue-2");
    expect(isWaiting(dependencies)).toBe(true);
  });

  it("removes a dependency and returns what is left", async () => {
    const { client, fetchMock } = respondWith({ blocked_by: [], blocks: [] });

    await client.removeIssueDependency("issue-2", "issue-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/issues/issue-2/dependencies/issue-1");
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("DELETE");
  });

  it("reads a parent's graph, and a malformed one as empty", async () => {
    const good = respondWith({ edges: [{ issue_id: "issue-2", blocked_by: ref("1", false) }] });
    const graph = await good.client.listSubIssueBlockers("parent");
    expect(good.fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/issues/parent/sub-issue-blockers");
    expect(graph.edges).toHaveLength(1);

    const bad = respondWith({ edges: 7 });
    await expect(bad.client.listSubIssueBlockers("parent")).resolves.toEqual({ edges: [] });
  });
});

describe("isWaiting", () => {
  it("is true only while some blocker is not done", () => {
    expect(isWaiting(undefined)).toBe(false);
    expect(isWaiting({ blocked_by: [] })).toBe(false);
    expect(isWaiting({ blocked_by: [ref("1", true)] })).toBe(false);
    expect(isWaiting({ blocked_by: [ref("1", true), ref("2", false)] })).toBe(true);
  });
});
