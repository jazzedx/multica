// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeDirectoryListing, RuntimeDirectoryRequest } from "../types";

const mockApi = vi.hoisted(() => ({
  listRuntimeDirectory: vi.fn(),
  getRuntimeDirectoryRequest: vi.fn(),
}));
vi.mock("../api", () => ({ api: mockApi }));

import {
  completeDirectoryNames,
  directoryBaseName,
  directoryHasEntry,
  directoryPath,
  isAbsoluteDirectoryPath,
  joinDirectoryPath,
  readRuntimeDirectory,
  splitDirectoryInput,
} from "./directories";

function listing(over: Partial<RuntimeDirectoryListing> = {}): RuntimeDirectoryListing {
  return {
    path: "/srv",
    separator: "/",
    entries: [],
    truncated: false,
    is_git_repo: false,
    problem: "",
    detail: "",
    ...over,
  };
}

function request(over: Partial<RuntimeDirectoryRequest>): RuntimeDirectoryRequest {
  return { id: "req-1", runtime_id: "rt-1", status: "pending", path: "/srv", listing: null, error: "", ...over };
}

afterEach(() => vi.clearAllMocks());

describe("splitDirectoryInput", () => {
  it.each([
    ["/srv/ap", "/srv/", "ap"],
    ["/srv/", "/srv/", ""],
    ["/", "/", ""],
    ["srv", "", "srv"],
    ["", "", ""],
    ["C:\\Users\\de", "C:\\Users\\", "de"],
    ["C:/Users/de", "C:/Users/", "de"],
  ])("%s", (text, parent, leaf) => {
    expect(splitDirectoryInput(text)).toEqual({ parent, leaf });
  });
});

describe("directoryPath", () => {
  it.each([
    ["/srv/app/", "/srv/app"],
    ["/srv/app", "/srv/app"],
    ["/", "/"],
    ["//", "/"],
    ["", ""],
    ["  ", ""],
    ["C:\\", "C:\\"],
    ["C:", "C:\\"],
    ["C:\\Users\\", "C:\\Users"],
  ])("%s", (text, want) => {
    expect(directoryPath(text)).toBe(want);
  });
});

it("recognises absolute paths of both kinds of machine", () => {
  expect(isAbsoluteDirectoryPath("/srv")).toBe(true);
  expect(isAbsoluteDirectoryPath("D:\\work")).toBe(true);
  expect(isAbsoluteDirectoryPath("\\\\host\\share")).toBe(true);
  expect(isAbsoluteDirectoryPath("srv/app")).toBe(false);
  expect(isAbsoluteDirectoryPath("~/app")).toBe(false);
});

it("joins a name onto a listing without doubling the separator", () => {
  expect(joinDirectoryPath(listing({ path: "/srv" }), "app")).toBe("/srv/app");
  expect(joinDirectoryPath(listing({ path: "/" }), "srv")).toBe("/srv");
  expect(joinDirectoryPath(listing({ path: "C:\\", separator: "\\" }), "work")).toBe("C:\\work");
  expect(joinDirectoryPath(listing({ path: "C:\\work", separator: "\\" }), "app")).toBe("C:\\work\\app");
});

it("names a path by its last segment", () => {
  expect(directoryBaseName("/srv/app/")).toBe("app");
  expect(directoryBaseName("C:\\work\\engine")).toBe("engine");
});

describe("completeDirectoryNames", () => {
  const entries = [".cache", ".config", "App", "api", "apps", "web"];

  it("keeps dot directories back until a dot is typed", () => {
    expect(completeDirectoryNames(entries, "")).toEqual(["App", "api", "apps", "web"]);
    expect(completeDirectoryNames(entries, ".c")).toEqual([".cache", ".config"]);
  });

  it("puts exact-case matches ahead of the rest", () => {
    expect(completeDirectoryNames(entries, "ap")).toEqual(["api", "apps", "App"]);
  });
});

it("compares Windows names without case", () => {
  expect(directoryHasEntry(listing({ entries: ["Work"] }), "work")).toBe(false);
  expect(directoryHasEntry(listing({ entries: ["Work"], separator: "\\" }), "work")).toBe(true);
});

describe("readRuntimeDirectory", () => {
  it("returns the listing the first call already carries", async () => {
    const found = listing({ entries: ["app"] });
    mockApi.listRuntimeDirectory.mockResolvedValue(request({ status: "completed", listing: found }));

    await expect(readRuntimeDirectory("rt-1", "/srv")).resolves.toEqual({ state: "listed", listing: found });
    expect(mockApi.getRuntimeDirectoryRequest).not.toHaveBeenCalled();
  });

  it("reads again while the machine has not answered", async () => {
    const found = listing();
    mockApi.listRuntimeDirectory.mockResolvedValue(request({ status: "pending" }));
    mockApi.getRuntimeDirectoryRequest
      .mockResolvedValueOnce(request({ status: "running" }))
      .mockResolvedValueOnce(request({ status: "completed", listing: found }));

    await expect(readRuntimeDirectory("rt-1", "/srv")).resolves.toEqual({ state: "listed", listing: found });
    expect(mockApi.getRuntimeDirectoryRequest).toHaveBeenCalledTimes(2);
  });

  it.each(["timeout", "failed"] as const)("is unavailable when the request ends as %s", async (status) => {
    mockApi.listRuntimeDirectory.mockResolvedValue(request({ status }));

    await expect(readRuntimeDirectory("rt-1", "/srv")).resolves.toEqual({ state: "unavailable" });
  });

  it("is unavailable when the machine cannot be asked", async () => {
    mockApi.listRuntimeDirectory.mockRejectedValue(new Error("runtime is offline"));

    await expect(readRuntimeDirectory("rt-1", "/srv")).resolves.toEqual({ state: "unavailable" });
  });
});
