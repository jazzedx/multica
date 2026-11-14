// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

const respondWith = (body: unknown) => {
  // A fresh Response per call: a body can only be read once.
  const fetchMock = vi.fn().mockImplementation(async () => Response.json(body));
  vi.stubGlobal("fetch", fetchMock);
  return { client: new ApiClient("https://api.example.test"), fetchMock };
};

describe("listSquadTemplates", () => {
  it("fills in what an older server leaves out", async () => {
    const { client } = respondWith([{ key: "bug-fix", agents: [{ name: "Leader" }], future_field: 1 }]);
    await expect(client.listSquadTemplates()).resolves.toMatchObject([
      {
        key: "bug-fix",
        recommended: false,
        title: { en: "", zh: "" },
        agents: [{ name: "Leader", role: "", skills: [] }],
        skills: [],
      },
    ]);
  });

  it.each([
    ["a template without a key", [{ title: { en: "Bug fix" } }]],
    ["a non-array body", { templates: [] }],
    ["a null body", null],
  ])("offers no templates for %s", async (_name, body) => {
    const { client } = respondWith(body);
    await expect(client.listSquadTemplates()).resolves.toEqual([]);
  });
});

describe("squad template plans", () => {
  const plan = {
    template: "bug-fix",
    dry_run: true,
    skills: [{ name: "verify", action: "create" }],
    agents: [{ name: "Leader", role: "Leader", action: "some-future-action" }],
    squad: { name: "Bug fix squad", action: "create" },
  };

  it("keeps actions it does not know and defaults the counts", async () => {
    const { client } = respondWith(plan);
    await expect(client.planSquadTemplate("bug-fix")).resolves.toMatchObject({
      agents: [{ action: "some-future-action", add_skills: 0 }],
      squad: { add_members: 0 },
    });
  });

  it("asks for a dry run when planning and not when installing", async () => {
    const { client, fetchMock } = respondWith(plan);
    await client.planSquadTemplate("bug-fix", { overwrite_modified_skills: true });
    await client.installSquadTemplate("bug-fix", { runtime_id: "rt-1" });

    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(init.body));
    expect(bodies).toEqual([{ overwrite_modified_skills: true, dry_run: true }, { runtime_id: "rt-1" }]);
    expect(fetchMock.mock.calls[0]?.[0]).toContain("/api/squad-templates/bug-fix/install");
  });

  // An empty plan would read as "nothing to install".
  it.each([
    ["a missing squad", { ...plan, squad: undefined }],
    ["a skill without an action", { ...plan, skills: [{ name: "verify" }] }],
    ["a null body", null],
  ])("fails for %s", async (_name, body) => {
    const { client } = respondWith(body);
    await expect(client.planSquadTemplate("bug-fix")).rejects.toThrow("malformed");
  });
});

describe("squad template removal plans", () => {
  const removal = {
    template: "bug-fix",
    dry_run: true,
    squad: { name: "Bug fix squad", action: "archive", id: "sq-1" },
    agents: [{ name: "Leader", action: "keep", reason: "some-future-reason" }],
    skills: [{ name: "verify", action: "delete" }],
  };

  it("keeps reasons it does not know", async () => {
    const { client } = respondWith(removal);
    await expect(client.planSquadTemplateRemoval("bug-fix")).resolves.toMatchObject({
      squad: { action: "archive" },
      agents: [{ action: "keep", reason: "some-future-reason" }],
    });
  });

  it("asks for a dry run when planning and not when uninstalling", async () => {
    const { client, fetchMock } = respondWith(removal);
    await client.planSquadTemplateRemoval("bug-fix", { remove_modified: true });
    await client.uninstallSquadTemplate("bug-fix", { remove_modified: true });

    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(init.body));
    expect(bodies).toEqual([{ remove_modified: true, dry_run: true }, { remove_modified: true }]);
    expect(fetchMock.mock.calls[0]?.[0]).toContain("/api/squad-templates/bug-fix/uninstall");
  });

  // An empty plan would read as "nothing to remove".
  it.each([
    ["a missing squad", { ...removal, squad: undefined }],
    ["an agent without an action", { ...removal, agents: [{ name: "Leader" }] }],
    ["a null body", null],
  ])("fails for %s", async (_name, body) => {
    const { client } = respondWith(body);
    await expect(client.planSquadTemplateRemoval("bug-fix")).rejects.toThrow("malformed");
  });
});
