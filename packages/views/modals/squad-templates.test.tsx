import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SquadTemplate, SquadTemplatePlan, SquadTemplateRemovalPlan } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { SquadTemplatesModal } from "./squad-templates";

const mocks = vi.hoisted(() => ({
  listSquadTemplates: vi.fn(),
  planSquadTemplate: vi.fn(),
  installSquadTemplate: vi.fn(),
  planSquadTemplateRemoval: vi.fn(),
  uninstallSquadTemplate: vi.fn(),
  push: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listSquadTemplates: mocks.listSquadTemplates,
    planSquadTemplate: mocks.planSquadTemplate,
    installSquadTemplate: mocks.installSquadTemplate,
    planSquadTemplateRemoval: mocks.planSquadTemplateRemoval,
    uninstallSquadTemplate: mocks.uninstallSquadTemplate,
    listRuntimes: async () => [],
    listMembers: async () => [],
  },
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (s: { user: { id: string } }) => unknown) => select({ user: { id: "me" } }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ squadDetail: (id: string) => `/acme/squads/${id}` }),
}));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push: mocks.push }) }));
vi.mock("sonner", () => ({ toast: { success: mocks.toastSuccess, error: mocks.toastError } }));
vi.mock("../agents/components/runtime-picker", () => ({
  RuntimePicker: ({ onSelect }: { onSelect: (id: string) => void }) => (
    <button type="button" onClick={() => onSelect("rt-1")}>
      Pick runtime
    </button>
  ),
}));

const TEMPLATES: SquadTemplate[] = [
  {
    key: "bug-fix",
    recommended: false,
    title: { en: "Bug fix", zh: "缺陷修复" },
    summary: { en: "Reproduce and fix.", zh: "复现并修复。" },
    squad_name: "Bug fix squad",
    squad_description: "",
    leader: "Leader",
    agents: [{ name: "Leader", role: "Leader", skills: ["verify"] }],
    skills: ["verify"],
  },
  {
    key: "delivery",
    recommended: true,
    title: { en: "Software development", zh: "软件开发" },
    summary: { en: "Requirement to deployment.", zh: "从需求到部署。" },
    squad_name: "Delivery squad",
    squad_description: "",
    leader: "Leader",
    agents: [
      { name: "Leader", role: "Leader", skills: ["verify"] },
      { name: "Tester", role: "Tester", skills: ["test"] },
    ],
    skills: ["verify", "test"],
  },
];

function plan(overrides: Partial<SquadTemplatePlan> = {}): SquadTemplatePlan {
  return {
    template: "delivery",
    dry_run: true,
    skills: [
      { name: "verify", action: "create" },
      { name: "test", action: "create" },
    ],
    agents: [
      { name: "Leader", role: "Leader", action: "create", add_skills: 0 },
      { name: "Tester", role: "Tester", action: "create", add_skills: 0 },
    ],
    squad: { name: "Delivery squad", action: "create", add_members: 0 },
    ...overrides,
  };
}

const onClose = vi.fn();

function renderModal(locale: "en" | "zh-Hans" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <SquadTemplatesModal onClose={onClose} />
    </QueryClientProvider>,
    { locale },
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listSquadTemplates.mockResolvedValue(TEMPLATES);
  mocks.planSquadTemplate.mockResolvedValue(plan());
  mocks.installSquadTemplate.mockResolvedValue(plan({ dry_run: false, squad: { name: "Delivery squad", action: "create", id: "sq-1", add_members: 0 } }));
});

describe("SquadTemplatesModal", () => {
  it("previews the recommended template and installs it on the chosen runtime", async () => {
    const user = userEvent.setup();
    renderModal();

    const agents = await screen.findByRole("region", { name: "Agents" });
    expect(screen.getByRole("button", { name: /Software development/, pressed: true })).toBeInTheDocument();
    expect(mocks.planSquadTemplate).toHaveBeenCalledWith("delivery", { overwrite_modified_skills: false });
    expect(within(agents).getByText("Tester")).toBeInTheDocument();

    const install = screen.getByRole("button", { name: "Set up squad" });
    expect(install).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Pick runtime" }));
    await user.click(install);

    await waitFor(() => expect(mocks.push).toHaveBeenCalledWith("/acme/squads/sq-1"));
    expect(mocks.installSquadTemplate).toHaveBeenCalledWith("delivery", {
      runtime_id: "rt-1",
      overwrite_modified_skills: undefined,
    });
    expect(onClose).toHaveBeenCalled();
  });

  it("previews another template when it is picked", async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(await screen.findByRole("button", { name: /Bug fix/ }));
    await waitFor(() =>
      expect(mocks.planSquadTemplate).toHaveBeenCalledWith("bug-fix", { overwrite_modified_skills: false }),
    );
  });

  it("replaces edited skills only when asked to", async () => {
    mocks.planSquadTemplate.mockImplementation(async (_key: string, options: { overwrite_modified_skills: boolean }) =>
      plan({
        skills: [
          { name: "verify", action: "unchanged" },
          { name: "test", action: options.overwrite_modified_skills ? "update" : "keep" },
        ],
      }),
    );
    const user = userEvent.setup();
    renderModal();

    const skills = await screen.findByRole("region", { name: "Skills" });
    expect(within(skills).getByText("Edited here, kept")).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: "Also replace skills that were edited in this workspace" }));
    expect(await within(skills).findByText("Update")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Pick runtime" }));
    await user.click(screen.getByRole("button", { name: "Set up squad" }));
    await waitFor(() =>
      expect(mocks.installSquadTemplate).toHaveBeenCalledWith("delivery", {
        runtime_id: "rt-1",
        overwrite_modified_skills: true,
      }),
    );
  });

  it("restores an archived agent without asking for a runtime", async () => {
    mocks.planSquadTemplate.mockResolvedValue(
      plan({
        agents: [
          { name: "Leader", role: "Leader", action: "exists", add_skills: 0 },
          { name: "Tester", role: "Tester", action: "restore", add_skills: 1 },
        ],
      }),
    );
    renderModal();

    const agents = await screen.findByRole("region", { name: "Agents" });
    expect(within(agents).getByText("Restore")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pick runtime" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set up squad" })).toBeEnabled();
  });

  it("needs no runtime when every agent already exists", async () => {
    mocks.planSquadTemplate.mockResolvedValue(
      plan({
        skills: [
          { name: "verify", action: "unchanged" },
          { name: "test", action: "unchanged" },
        ],
        agents: [
          { name: "Leader", role: "Leader", action: "exists", add_skills: 0 },
          { name: "Tester", role: "Tester", action: "exists", add_skills: 1 },
        ],
        squad: { name: "Delivery squad", action: "exists", id: "sq-1", add_members: 1 },
      }),
    );
    const user = userEvent.setup();
    renderModal();

    expect(await screen.findByText("Skills to add: 1")).toBeInTheDocument();
    expect(screen.getByText("Members to add: 1")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pick runtime" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Set up squad" }));
    await waitFor(() =>
      expect(mocks.installSquadTemplate).toHaveBeenCalledWith("delivery", {
        runtime_id: undefined,
        overwrite_modified_skills: undefined,
      }),
    );
  });

  it("opens the squad instead of installing a template that is already set up", async () => {
    mocks.planSquadTemplate.mockResolvedValue(
      plan({
        skills: [
          { name: "verify", action: "unchanged" },
          { name: "test", action: "keep" },
        ],
        agents: [
          { name: "Leader", role: "Leader", action: "exists", add_skills: 0 },
          { name: "Tester", role: "Tester", action: "exists", add_skills: 0 },
        ],
        squad: { name: "Delivery squad", action: "exists", id: "sq-1", add_members: 0 },
      }),
    );
    const user = userEvent.setup();
    renderModal();

    await user.click(await screen.findByRole("button", { name: "Open squad" }));
    expect(mocks.push).toHaveBeenCalledWith("/acme/squads/sq-1");
    expect(mocks.installSquadTemplate).not.toHaveBeenCalled();
  });

  it("keeps the dialog open and says why when the install fails", async () => {
    mocks.installSquadTemplate.mockRejectedValue(new Error('agent "Tester": this runtime is private'));
    const user = userEvent.setup();
    renderModal();

    await screen.findByRole("region", { name: "Agents" });
    await user.click(screen.getByRole("button", { name: "Pick runtime" }));
    await user.click(screen.getByRole("button", { name: "Set up squad" }));

    await waitFor(() => expect(mocks.toastError).toHaveBeenCalledWith('agent "Tester": this runtime is private'));
    expect(onClose).not.toHaveBeenCalled();
    // What the failed install finished is picked up by a fresh plan.
    await waitFor(() => expect(mocks.planSquadTemplate.mock.calls.length).toBeGreaterThan(1));
  });

  it("shows the catalog's Chinese copy in a Chinese UI", async () => {
    renderModal("zh-Hans");
    expect(await screen.findByRole("button", { name: /软件开发/ })).toBeInTheDocument();
    expect(screen.getByText("从需求到部署。")).toBeInTheDocument();
  });
});

function removal(overrides: Partial<SquadTemplateRemovalPlan> = {}): SquadTemplateRemovalPlan {
  return {
    template: "delivery",
    dry_run: true,
    squad: { name: "Delivery squad", action: "archive", id: "sq-1" },
    agents: [
      { name: "Leader", action: "archive", id: "a-1" },
      { name: "Tester", action: "archive", id: "a-2" },
    ],
    skills: [
      { name: "verify", action: "delete", id: "s-1" },
      { name: "test", action: "delete", id: "s-2" },
    ],
    ...overrides,
  };
}

describe("SquadTemplatesModal uninstall", () => {
  const installedPlan = plan({
    skills: [
      { name: "verify", action: "unchanged" },
      { name: "test", action: "unchanged" },
    ],
    agents: [
      { name: "Leader", role: "Leader", action: "exists", add_skills: 0 },
      { name: "Tester", role: "Tester", action: "exists", add_skills: 0 },
    ],
    squad: { name: "Delivery squad", action: "exists", id: "sq-1", add_members: 0 },
  });

  async function openUninstall(user: ReturnType<typeof userEvent.setup>) {
    mocks.planSquadTemplate.mockResolvedValue(installedPlan);
    renderModal();
    await user.click(await screen.findByRole("button", { name: "Uninstall..." }));
  }

  beforeEach(() => {
    mocks.planSquadTemplateRemoval.mockResolvedValue(removal());
    mocks.uninstallSquadTemplate.mockResolvedValue(removal({ dry_run: false }));
  });

  it("is not offered for a template the workspace holds nothing of", async () => {
    renderModal();
    await screen.findByRole("region", { name: "Agents" });
    expect(screen.queryByRole("button", { name: "Uninstall..." })).not.toBeInTheDocument();
  });

  it("previews the removal and uninstalls on confirmation", async () => {
    const user = userEvent.setup();
    await openUninstall(user);

    expect(await screen.findByRole("heading", { name: "Uninstall Software development" })).toBeInTheDocument();
    expect(within(await screen.findByRole("region", { name: "Skills" })).getAllByText("Delete")).toHaveLength(2);
    expect(mocks.uninstallSquadTemplate).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Uninstall" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(mocks.uninstallSquadTemplate).toHaveBeenCalledWith("delivery", { remove_modified: undefined });
    expect(mocks.toastSuccess).toHaveBeenCalledWith("Template uninstalled");
  });

  it("says why something stays and removes changed things only when asked to", async () => {
    mocks.planSquadTemplateRemoval.mockImplementation(async (_key: string, options: { remove_modified: boolean }) =>
      removal({
        agents: [
          { name: "Leader", action: "keep", reason: "in_squad", id: "a-1" },
          options.remove_modified
            ? { name: "Tester", action: "archive", id: "a-2" }
            : { name: "Tester", action: "keep", reason: "modified", id: "a-2" },
        ],
        skills: [
          { name: "verify", action: "keep", reason: "in_use", id: "s-1" },
          { name: "test", action: "keep", reason: "some-future-reason", id: "s-2" },
        ],
      }),
    );
    const user = userEvent.setup();
    await openUninstall(user);

    const agents = await screen.findByRole("region", { name: "Agents" });
    expect(within(agents).getByText("Kept: still in a squad")).toBeInTheDocument();
    expect(within(agents).getByText("Kept: changed here")).toBeInTheDocument();
    const skills = screen.getByRole("region", { name: "Skills" });
    expect(within(skills).getByText("Kept: an agent still uses it")).toBeInTheDocument();
    expect(within(skills).getByText("Kept: some-future-reason")).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: "Also remove what was changed after it was set up" }));
    await waitFor(() => expect(within(agents).queryByText("Kept: changed here")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Uninstall" }));
    await waitFor(() =>
      expect(mocks.uninstallSquadTemplate).toHaveBeenCalledWith("delivery", { remove_modified: true }),
    );
  });

  it("does not offer an uninstall that would remove nothing", async () => {
    mocks.planSquadTemplateRemoval.mockResolvedValue(
      removal({
        squad: { name: "Delivery squad", action: "absent" },
        agents: [
          { name: "Leader", action: "keep", reason: "open_issues", id: "a-1" },
          { name: "Tester", action: "absent" },
        ],
        skills: [
          { name: "verify", action: "keep", reason: "in_use", id: "s-1" },
          { name: "test", action: "keep", reason: "not_installed", id: "s-2" },
        ],
      }),
    );
    const user = userEvent.setup();
    await openUninstall(user);

    expect(await screen.findByText("Nothing of this template can be removed.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Uninstall" })).toBeDisabled();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("goes back to the install view", async () => {
    const user = userEvent.setup();
    await openUninstall(user);

    await user.click(await screen.findByRole("button", { name: "Back" }));
    expect(await screen.findByRole("button", { name: "Open squad" })).toBeInTheDocument();
    expect(mocks.uninstallSquadTemplate).not.toHaveBeenCalled();
  });

  it("keeps the dialog open and says why when the uninstall fails", async () => {
    mocks.uninstallSquadTemplate.mockRejectedValue(new Error('agent "Tester": only the agent owner can manage this agent'));
    const user = userEvent.setup();
    await openUninstall(user);

    await user.click(await screen.findByRole("button", { name: "Uninstall" }));
    await waitFor(() =>
      expect(mocks.toastError).toHaveBeenCalledWith('agent "Tester": only the agent owner can manage this agent'),
    );
    expect(onClose).not.toHaveBeenCalled();
  });
});
