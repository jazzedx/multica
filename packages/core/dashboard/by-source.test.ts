// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";
import { dashboardUsageBySourceKey } from "./by-source";
import { dashboardKeys } from "./queries";

afterEach(() => vi.unstubAllGlobals());

describe("getDashboardUsageBySource", () => {
  const respondWith = (body: unknown) => {
    const fetchMock = vi.fn().mockImplementation(async () => Response.json(body));
    vi.stubGlobal("fetch", fetchMock);
    return { client: new ApiClient("https://api.example.test"), fetchMock };
  };

  it("passes the dashboard's window, project and timezone", async () => {
    const { client, fetchMock } = respondWith([]);
    await client.getDashboardUsageBySource({ days: 7, project_id: "project-1", tz: "Asia/Shanghai" });

    const request = new URL(fetchMock.mock.calls[0]![0]);
    expect(request.pathname).toBe("/api/dashboard/usage/by-source");
    expect(Object.fromEntries(request.searchParams)).toEqual({
      days: "7",
      project_id: "project-1",
      tz: "Asia/Shanghai",
    });
  });

  it("keeps a source it does not know and fills in what an older server leaves out", async () => {
    const { client } = respondWith([{ source: "some-future-source", model: "m", input_tokens: 5 }]);
    await expect(client.getDashboardUsageBySource({})).resolves.toMatchObject([
      { source: "some-future-source", provider: "", output_tokens: 0, task_count: 0 },
    ]);
  });

  it.each([
    ["a row without a source", [{ model: "m", input_tokens: 5 }]],
    ["a non-array body", { rows: [] }],
    ["a null body", null],
  ])("reads %s as an empty breakdown", async (_name, body) => {
    const { client } = respondWith(body);
    await expect(client.getDashboardUsageBySource({})).resolves.toEqual([]);
  });
});

describe("dashboardUsageBySourceKey", () => {
  // The dashboard's refresh button invalidates everything under this prefix.
  it("sits under the workspace's dashboard key", () => {
    const key = dashboardUsageBySourceKey("ws-1", 7, null, "UTC");
    expect(key.slice(0, 2)).toEqual([...dashboardKeys.all("ws-1")]);
    expect(key).not.toEqual(dashboardUsageBySourceKey("ws-1", 30, null, "UTC"));
  });

  // A limit change must not show a result cached under the old one.
  it("carries the history limit the query is made under", () => {
    expect(dashboardUsageBySourceKey("ws-1", 7, null, "UTC", 30)[6]).toBe(30);
    expect(dashboardUsageBySourceKey("ws-1", 7, null, "UTC")[6]).toBeNull();
  });
});
