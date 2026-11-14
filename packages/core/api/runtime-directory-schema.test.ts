// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import { RuntimeDirectoryRequestSchema } from "./schemas";

const endpoint = "POST /api/runtimes/:id/directories";
const fallback = {
  id: "",
  runtime_id: "rt-1",
  status: "failed" as const,
  path: "/srv",
  listing: null,
  error: "",
};

describe("RuntimeDirectoryRequestSchema", () => {
  it("fills in what an answer leaves out", () => {
    const parsed = parseWithFallback(
      { id: "req-1", status: "completed", listing: { path: "/srv", entries: ["app"] } },
      RuntimeDirectoryRequestSchema,
      fallback,
      { endpoint },
    );
    expect(parsed).toMatchObject({
      id: "req-1",
      status: "completed",
      error: "",
      listing: { path: "/srv", separator: "/", entries: ["app"], truncated: false, is_git_repo: false, problem: "", detail: "" },
    });
  });

  it("reads a status or problem it does not know as a failure", () => {
    const parsed = parseWithFallback(
      { id: "req-1", status: "paused", listing: { path: "/srv", problem: "quota", separator: ":" } },
      RuntimeDirectoryRequestSchema,
      fallback,
      { endpoint },
    );
    expect(parsed.status).toBe("failed");
    expect(parsed.listing).toMatchObject({ problem: "error", separator: "/" });
  });

  it("falls back to a failed request for a malformed body", () => {
    expect(parseWithFallback({ status: "completed" }, RuntimeDirectoryRequestSchema, fallback, { endpoint })).toEqual(fallback);
    expect(parseWithFallback(null, RuntimeDirectoryRequestSchema, fallback, { endpoint })).toEqual(fallback);
    expect(
      parseWithFallback({ id: "req-1", status: "completed", listing: { entries: "nope" } }, RuntimeDirectoryRequestSchema, fallback, { endpoint }),
    ).toEqual(fallback);
  });
});
