// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";
import { leadTime } from "./delivery";

afterEach(() => {
  vi.unstubAllGlobals();
});

const respondWith = (body: unknown) => {
  const fetchMock = vi.fn().mockImplementation(async () => Response.json(body));
  vi.stubGlobal("fetch", fetchMock);
  return { client: new ApiClient("https://api.example.test"), fetchMock };
};

describe("getIssueDeliveryOverview", () => {
  it("reads the overview of an issue", async () => {
    const { client, fetchMock } = respondWith({
      sub_issues: { total: 4, done: 3, cancelled: 1 },
      runs: { total: 17, failed: 0, active: 1 },
      rework_rounds: 2,
      human_actions: 1,
      pull_requests_merged: 4,
      started_at: "2026-01-01T10:00:00Z",
      finished_at: "2026-01-01T10:34:00Z",
    });

    const overview = await client.getIssueDeliveryOverview("issue-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/issues/issue-1/delivery-overview");
    expect(overview.sub_issues).toEqual({ total: 4, done: 3, cancelled: 1 });
    expect(overview.rework_rounds).toBe(2);
    expect(overview.finished_at).toBe("2026-01-01T10:34:00Z");
  });

  it("fills in what an older or partial response leaves out", async () => {
    const { client } = respondWith({ runs: { total: 3 }, started_at: "2026-01-01T10:00:00Z" });
    const overview = await client.getIssueDeliveryOverview("issue-1");
    expect(overview.sub_issues).toEqual({ total: 0, done: 0, cancelled: 0 });
    expect(overview.runs).toEqual({ total: 3, failed: 0, active: 0 });
    expect(overview.finished_at).toBeNull();
  });

  // Nothing to show is the right reading of a body that cannot be read.
  it("reads a malformed body as an empty overview", async () => {
    const { client } = respondWith({ sub_issues: "nope" });
    const overview = await client.getIssueDeliveryOverview("issue-1");
    expect(overview.sub_issues.total).toBe(0);
    expect(overview.runs.total).toBe(0);
  });
});

describe("leadTime", () => {
  const start = "2026-01-01T10:00:00Z";
  const at = (iso: string) => Date.parse(iso);

  it("uses the largest two units that matter", () => {
    expect(leadTime(start, "2026-01-01T10:00:40Z", 0)).toEqual({ unit: "under_minute" });
    expect(leadTime(start, "2026-01-01T10:34:59Z", 0)).toEqual({ unit: "minutes", minutes: 34 });
    expect(leadTime(start, "2026-01-01T12:05:00Z", 0)).toEqual({ unit: "hours", hours: 2, minutes: 5 });
    expect(leadTime(start, "2026-01-04T13:30:00Z", 0)).toEqual({ unit: "days", days: 3, hours: 3 });
  });

  it("runs to now while the work is not finished", () => {
    expect(leadTime(start, null, at("2026-01-01T10:20:00Z"))).toEqual({ unit: "minutes", minutes: 20 });
  });

  it("is nothing for times it cannot read or that run backwards", () => {
    expect(leadTime("not a time", null, at(start))).toBeNull();
    expect(leadTime(start, "2026-01-01T09:00:00Z", 0)).toBeNull();
  });
});
