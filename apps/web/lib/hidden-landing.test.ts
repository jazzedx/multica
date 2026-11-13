import { describe, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { proxy } from "../proxy";
import { LANDING_PATH_PREFIXES, LANDING_ROUTES } from "./landing-locale-path";

const HOST = "https://192.168.1.10:3080";
const session = { multica_logged_in: "1", last_workspace_slug: "acme" };

function location(path: string, cookies: Record<string, string> = {}) {
  const cookie = Object.entries(cookies)
    .map(([key, value]) => `${key}=${value}`)
    .join("; ");
  const req = new NextRequest(`${HOST}${path}`, {
    headers: cookie ? { cookie } : undefined,
  });
  return proxy(req).headers.get("location");
}

const prefixes = Object.keys(LANDING_PATH_PREFIXES);
const hiddenRoutes = [...LANDING_ROUTES];
const hiddenPaths = [
  "/",
  ...prefixes.map((p) => `/${p}`),
  ...hiddenRoutes.map((r) => `/${r}`),
  ...hiddenRoutes.map((r) => `/${r}/nested`),
  ...prefixes.flatMap((p) => hiddenRoutes.map((r) => `/${p}/${r}`)),
];

describe("hidden landing pages", () => {
  it.each(hiddenPaths)("sends a logged-out %s visit to login", (path) => {
    expect(location(path)).toBe(`${HOST}/login`);
  });

  it.each(hiddenPaths)("sends a logged-in %s visit to the last workspace", (path) => {
    expect(location(path, session)).toBe(`${HOST}/acme/issues`);
  });

  it("sends a logged-in visit without a last workspace to login", () => {
    expect(location("/pricing", { multica_logged_in: "1" })).toBe(`${HOST}/login`);
  });

  it.each(["/download", "/licensing", "/zh/download", "/ja/licensing"])(
    "hides %s",
    (path) => {
      expect(location(path)).toBe(`${HOST}/login`);
      expect(location(path, session)).toBe(`${HOST}/acme/issues`);
    },
  );

  it("keeps a workspace whose slug is a language prefix reachable for its members", () => {
    expect(location("/zh", { multica_logged_in: "1", last_workspace_slug: "zh" })).toBeNull();
  });

  it.each(["/login", "/acme/issues", "/invite/abc"])("leaves app route %s alone", (path) => {
    expect(location(path)).toBeNull();
  });
});
