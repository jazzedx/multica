import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { RuntimeDirectoryListing } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import en from "../../locales/en/projects.json";

const state = vi.hoisted(() => ({
  runtimes: [] as unknown[],
  createMock: vi.fn(),
  listRuntimeDirectory: vi.fn(),
  getRuntimeDirectoryRequest: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listRuntimeDirectory: state.listRuntimeDirectory,
    getRuntimeDirectoryRequest: state.getRuntimeDirectoryRequest,
  },
}));
vi.mock("@multica/core/projects", () => ({
  useCreateProjectResource: () => ({ mutateAsync: state.createMock, isPending: false }),
}));
vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (s: { localWorktreeSupported: boolean }) => unknown) =>
    selector({ localWorktreeSupported: true }),
}));
vi.mock("@multica/core/runtimes", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/runtimes")>()),
  runtimeListOptions: () => ({
    queryKey: ["runtimes", "workspace-1"],
    queryFn: async () => state.runtimes,
  }),
  runtimeAdvertisesLocalWorktree: () => true,
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import {
  RemoteDirectoryAdder,
  directoryMachines,
  directoryStatus,
} from "./remote-directory-adder";

const strings = en.resources;
const NOW = Date.now();

function runtime(daemonId: string | null, host: string, status: "online" | "offline", mode = "local") {
  return {
    id: `rt-${daemonId ?? host}`,
    workspace_id: "workspace-1",
    daemon_id: daemonId,
    name: `Codex (${host})`,
    runtime_mode: mode,
    provider: "codex",
    launch_header: "",
    status,
    device_info: `${host} · codex 1.0.0`,
    metadata: {},
    owner_id: "u1",
    visibility: "private",
    last_seen_at: new Date(NOW - (status === "online" ? 10_000 : 3_600_000)).toISOString(),
    created_at: "2026-05-17T11:00:00Z",
    updated_at: "2026-05-17T11:00:00Z",
  };
}

function folder(daemonId: string) {
  return {
    id: `folder-${daemonId}`,
    project_id: "p1",
    workspace_id: "workspace-1",
    resource_type: "local_directory",
    resource_ref: { local_path: "/srv/old", daemon_id: daemonId },
    label: null,
    position: 0,
    created_at: "2026-08-18T00:00:00Z",
    created_by: "u1",
  };
}

function listing(over: Partial<RuntimeDirectoryListing>): RuntimeDirectoryListing {
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

/** The directories of the pretend computer, by path. */
function serve(tree: Record<string, Partial<RuntimeDirectoryListing>>) {
  state.listRuntimeDirectory.mockImplementation(
    async (runtimeId: string, path: string, options: { probeWrite?: boolean } = {}) => {
      const found = tree[path];
      return {
        id: "req",
        runtime_id: runtimeId,
        status: "completed",
        path,
        error: "",
        listing: found
          ? listing({ path: path || "/home/dev", ...found, ...(options.probeWrite ? {} : { problem: found.problem === "not_writable" ? "" : (found.problem ?? "") }) })
          : listing({ path, problem: "not_found" }),
      };
    },
  );
}

function renderAdder(folders: unknown[] = []) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <RemoteDirectoryAdder
        wsId="workspace-1"
        projectId="p1"
        folders={folders as never}
      />
    </QueryClientProvider>,
  );
}

async function openDialog() {
  fireEvent.click(await screen.findByRole("button", { name: strings.remote_add }));
  return (await screen.findByRole("combobox", { name: strings.remote_path_label })) as HTMLInputElement;
}

beforeEach(() => {
  state.runtimes = [runtime("daemon-1", "build-server", "online")];
  state.createMock.mockReset().mockResolvedValue({});
  state.listRuntimeDirectory.mockReset();
  state.getRuntimeDirectoryRequest.mockReset();
});

describe("directoryMachines", () => {
  it("lists computers with a daemon and marks the ones that already have a folder", () => {
    const machines = directoryMachines(
      [
        runtime("daemon-1", "build-server", "online"),
        runtime("daemon-2", "render-box", "offline"),
        runtime(null, "cloud", "online", "cloud"),
      ] as never,
      [folder("daemon-2")] as never,
      NOW,
    );
    expect(machines).toEqual([
      expect.objectContaining({ daemonId: "daemon-1", online: true, runtimeId: "rt-daemon-1", taken: false }),
      expect.objectContaining({ daemonId: "daemon-2", online: false, runtimeId: null, taken: true }),
    ]);
  });
});

describe("directoryStatus", () => {
  const srv = { state: "listed" as const, listing: listing({ path: "/srv", entries: ["app"] }) };

  it.each([
    ["nothing typed", { text: "", canAsk: true, parent: undefined, target: undefined }, { kind: "empty" }],
    ["a relative path", { text: "srv/app", canAsk: false, parent: undefined, target: undefined }, { kind: "problem", problem: "not_absolute" }],
    ["a computer that cannot be asked", { text: "/srv/app", canAsk: false, parent: undefined, target: undefined }, { kind: "unknown" }],
    ["a listing still on its way", { text: "/srv/app", canAsk: true, parent: undefined, target: undefined }, { kind: "reading" }],
    ["a computer that did not answer", { text: "/srv/app", canAsk: true, parent: { state: "unavailable" as const }, target: undefined }, { kind: "unknown" }],
    ["a name the directory does not hold", { text: "/srv/nope", canAsk: true, parent: srv, target: undefined }, { kind: "problem", problem: "not_found" }],
    ["a name that exists, not yet read", { text: "/srv/app", canAsk: true, parent: srv, target: undefined }, { kind: "reading" }],
    [
      "a path under a directory that does not exist",
      { text: "/nope/app", canAsk: true, parent: { state: "listed" as const, listing: listing({ path: "/nope", problem: "not_found" }) }, target: undefined },
      { kind: "problem", problem: "not_found" },
    ],
  ])("%s", (_name, input, want) => {
    expect(directoryStatus(input)).toEqual(want);
  });

  it("does not call a name missing when the listing was cut short", () => {
    const cut = { state: "listed" as const, listing: listing({ entries: ["a"], truncated: true }) };
    expect(directoryStatus({ text: "/srv/zzz", canAsk: true, parent: cut, target: undefined })).toEqual({ kind: "unknown" });
  });

  it("judges a path ending in a separator by its own listing", () => {
    const home = { state: "listed" as const, listing: listing({ path: "/home/dev", problem: "protected" }) };
    expect(directoryStatus({ text: "/home/dev/", canAsk: true, parent: home, target: undefined })).toEqual({ kind: "problem", problem: "protected" });
  });
});

describe("RemoteDirectoryAdder", () => {
  it("renders nothing when every computer already has a folder", async () => {
    const { container } = renderAdder([folder("daemon-1")]);
    await waitFor(() => expect(state.listRuntimeDirectory).not.toHaveBeenCalled());
    expect(container.querySelector("button")).toBeNull();
  });

  it("completes a path level by level and adds the folder it ends on", async () => {
    serve({
      "": { path: "/home/dev", entries: ["code"], problem: "protected" },
      "/home/dev": { entries: ["code", "notes"], problem: "protected" },
      "/home/dev/code": { entries: ["engine-core"] },
      "/home/dev/code/engine-core": { entries: ["src"], is_git_repo: true },
    });
    renderAdder();
    const input = await openDialog();

    // Nothing typed: the home directory is offered as a place to start.
    fireEvent.click(await screen.findByRole("option", { name: "/home/dev" }));
    expect(input.value).toBe("/home/dev/");

    // Typing narrows the names of that level without asking again.
    await screen.findByRole("option", { name: "notes" });
    const asked = state.listRuntimeDirectory.mock.calls.length;
    fireEvent.change(input, { target: { value: "/home/dev/co" } });
    expect(screen.getByRole("option", { name: "code" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "notes" })).not.toBeInTheDocument();
    expect(state.listRuntimeDirectory.mock.calls.length).toBe(asked);

    // Tab takes the first suggestion and moves a level down.
    fireEvent.keyDown(input, { key: "Tab" });
    expect(input.value).toBe("/home/dev/code/");
    await screen.findByRole("option", { name: "engine-core" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input.value).toBe("/home/dev/code/engine-core/");

    expect(await screen.findByText(strings.remote_status_ok_git)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: strings.mode_add }));

    await waitFor(() => expect(state.createMock).toHaveBeenCalledTimes(1));
    expect(state.createMock).toHaveBeenCalledWith({
      resource_type: "local_directory",
      resource_ref: {
        local_path: "/home/dev/code/engine-core",
        daemon_id: "daemon-1",
        label: "engine-core",
        // A git repository on a daemon that supports it starts on parallel runs.
        execution_mode: "worktree",
      },
    });
    // Writing was tried once, on the folder being added.
    const probes = state.listRuntimeDirectory.mock.calls.filter((call) => call[2]?.probeWrite);
    expect(probes.map((call) => call[1])).toEqual(["/home/dev/code/engine-core"]);
  });

  it("keeps the folder out when the computer cannot write to it", async () => {
    serve({
      "": { path: "/home/dev", entries: [], problem: "protected" },
      "/srv": { entries: ["locked"] },
      "/srv/locked": { entries: [], problem: "not_writable" },
    });
    renderAdder();
    const input = await openDialog();
    fireEvent.change(input, { target: { value: "/srv/locked" } });
    expect(await screen.findByText(strings.remote_status_ok)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: strings.mode_add }));

    expect(await screen.findByRole("alert")).toHaveTextContent(strings.local_validate_not_writable);
    expect(state.createMock).not.toHaveBeenCalled();
  });

  it("still adds a typed path when the computer does not answer", async () => {
    state.listRuntimeDirectory.mockResolvedValue({
      id: "req", runtime_id: "rt-daemon-1", status: "timeout", path: "", listing: null, error: "",
    });
    renderAdder();
    const input = await openDialog();
    fireEvent.change(input, { target: { value: "/srv/app/" } });

    expect(await screen.findByText(strings.remote_unavailable)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: strings.mode_add }));

    await waitFor(() => expect(state.createMock).toHaveBeenCalledTimes(1));
    // One unanswered question is enough: the path typed after it is not asked about.
    expect(state.listRuntimeDirectory).toHaveBeenCalledTimes(1);
    expect(state.createMock.mock.calls[0]![0].resource_ref).toMatchObject({
      local_path: "/srv/app",
      daemon_id: "daemon-1",
      execution_mode: "in_place",
    });
  });

  it("does not offer to add a path that does not exist", async () => {
    serve({ "": { path: "/home/dev", entries: [] }, "/srv": { entries: ["app"] } });
    renderAdder();
    const input = await openDialog();
    fireEvent.change(input, { target: { value: "/srv/nope" } });

    expect(await screen.findByText(strings.local_validate_not_found)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: strings.mode_add })).toBeDisabled();
  });
});
