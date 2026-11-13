import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { api } from "@multica/core/api";
import {
  AgentChannelsSection,
  AgentChannelLinks,
} from "../../agents/components/agent-channels-section";
import { ChannelsTab } from "./channels-tab";
import { ChannelConnectDialog } from "./channel-connect-dialog";

const state = vi.hoisted(() => ({
  role: "admin",
  member: true,
  search: "tab=channels",
  replace: vi.fn(),
  open: vi.fn(),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({
    role: state.role,
    userId: "user-1",
    member: state.member ? { role: state.role } : null,
  }),
}));
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/acme/agents/${id}`,
  }),
}));
vi.mock("../../navigation/context", async (original) => ({
  ...(await original<typeof import("../../navigation/context")>()),
  useNavigation: () => ({
    pathname: "/acme/settings",
    searchParams: new URLSearchParams(state.search),
    replace: state.replace,
    push: vi.fn(),
  }),
}));
vi.mock("../../platform", () => ({ openExternal: state.open }));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`avatar-${actorId}`} />
  ),
}));
vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api: Object.fromEntries(
    [
      "listLarkInstallations",
      "deleteLarkInstallation",
      "listAgents",
      "beginLarkInstall",
      "getLarkInstallStatus",
    ].map((name) => [name, vi.fn()]),
  ),
}));

const agent = {
  id: "agent-1",
  name: "Mika",
  owner_id: "user-1",
  archived_at: null,
} as Agent;
const otherAgent = {
  ...agent,
  id: "agent-2",
  name: "Support",
  owner_id: "user-2",
};
const active = {
  id: "i1",
  workspace_id: "ws-1",
  agent_id: agent.id,
  status: "active",
};
const listing = (installations: unknown[] = []) => ({
  configured: true,
  install_supported: true,
  installations,
});

function show(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return {
    ...renderWithI18n(
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>,
    ),
    qc,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  state.role = "admin";
  state.member = true;
  state.search = "tab=channels";
  vi.mocked(api.listLarkInstallations).mockResolvedValue(listing() as never);
  vi.mocked(api.listAgents).mockResolvedValue([agent, otherAgent]);
  vi.mocked(api.beginLarkInstall).mockResolvedValue({
    session_id: "s1",
    qr_code_url: "https://accounts.feishu.cn/example",
    expires_in_seconds: 300,
    poll_interval_seconds: 2,
  });
});

describe("agent messaging channels", () => {
  it.each([
    ["feishu", "open.feishu.cn", "Manage in Feishu"],
    ["lark", "open.larksuite.com", "Manage in Lark"],
    [undefined, "open.feishu.cn", "Manage in Feishu"],
  ])(
    "keeps management and troubleshooting available for an existing %s bot",
    async (region, host, label) => {
      vi.mocked(api.listLarkInstallations).mockResolvedValue({
        ...listing([{ ...active, app_id: "cli_a&b", region }]),
        install_supported: false,
      } as never);
      show(<AgentChannelsSection agent={agent} />);
      fireEvent.click(
        await screen.findByRole("button", { name: "Manage Lark" }),
      );
      fireEvent.click(await screen.findByRole("menuitem", { name: label }));
      expect(state.open).toHaveBeenCalledWith(`https://${host}/app/cli_a%26b`);
      fireEvent.click(screen.getByRole("button", { name: "Manage Lark" }));
      fireEvent.click(
        await screen.findByRole("menuitem", { name: "Troubleshooting" }),
      );
      expect(state.open).toHaveBeenCalledWith(
        "https://multica.ai/docs/lark-bot-integration",
      );
    },
  );

  it("keeps recovery controls hidden for members who cannot manage the bot", async () => {
    state.role = "member";
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([
        {
          ...active,
          agent_id: otherAgent.id,
          app_id: "cli_readonly",
          first_message_received_at: "2026-11-10T00:00:00Z",
        },
      ]) as never,
    );
    show(<AgentChannelsSection agent={otherAgent} />);
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage Lark" })).toBeNull();
  });

  it("does not offer management to someone without workspace membership", async () => {
    state.member = false;
    const { container } = show(<AgentChannelsSection agent={agent} />);
    expect(
      screen.queryByRole("button", { name: "Add messaging channel" }),
    ).toBeNull();
    expect(container).toBeEmptyDOMElement();
    expect(api.listLarkInstallations).not.toHaveBeenCalled();
  });

  it("hides the empty section from a member who cannot manage the agent", async () => {
    state.role = "member";
    const { container } = show(<AgentChannelsSection agent={otherAgent} />);
    await waitFor(() => expect(api.listLarkInstallations).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("lets an agent owner connect Feishu", async () => {
    state.role = "member";
    show(<AgentChannelsSection agent={agent} />);
    await waitFor(() =>
      expect(
        screen.getAllByRole("button", { name: "Add messaging channel" }).length,
      ).toBeGreaterThan(0),
    );
    fireEvent.click(
      screen.getAllByRole("button", { name: "Add messaging channel" })[0]!,
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByRole("button", { name: /^Feishu$/ }),
    ).toBeEnabled();
  });

  it("shows a chat link to a read-only member", async () => {
    state.role = "member";
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([
        {
          ...active,
          agent_id: otherAgent.id,
          app_id: "cli_mika",
          region: "feishu",
        },
      ]) as never,
    );
    show(
      <>
        <AgentChannelLinks agent={otherAgent} />
        <AgentChannelsSection agent={otherAgent} />
      </>,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Find me on Lark" }),
    );
    expect(state.open).toHaveBeenCalledWith(
      "https://applink.feishu.cn/client/bot/open?appId=cli_mika",
    );
    expect(screen.queryByRole("button", { name: "Manage Lark" })).toBeNull();
  });

  it("updates Lark from waiting to connected when inbound evidence arrives", async () => {
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([{ ...active, app_id: "cli_mika" }]) as never,
    );
    const { qc } = show(<AgentChannelsSection agent={agent} />);
    expect(
      await screen.findByText("Waiting for first message"),
    ).toBeInTheDocument();
    qc.setQueryData(
      ["lark", "ws-1", "installations"],
      listing([
        {
          ...active,
          app_id: "cli_mika",
          first_message_received_at: "2026-11-10T00:00:00Z",
        },
      ]),
    );
    await waitFor(() =>
      expect(screen.queryByText("Waiting for first message")).toBeNull(),
    );
    expect(screen.getByText("Connected")).toBeInTheDocument();
  });

  it("does not display revoked connections or another agent's connection", async () => {
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([
        { ...active, status: "revoked" },
        { ...active, id: "i2", agent_id: "agent-2" },
      ]) as never,
    );
    show(<AgentChannelsSection agent={agent} />);
    await screen.findAllByRole("button", { name: "Add messaging channel" });
    expect(screen.queryByText("Waiting for first message")).toBeNull();
  });

  it("confirms Feishu disconnect and preserves the connection on failure", async () => {
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([active]) as never,
    );
    vi.mocked(api.deleteLarkInstallation)
      .mockRejectedValueOnce(new Error("Disconnect failed"))
      .mockResolvedValueOnce(undefined);
    show(<AgentChannelsSection agent={agent} />);
    fireEvent.click(await screen.findByRole("button", { name: /^Manage / }));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Disconnect" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(api.deleteLarkInstallation).not.toHaveBeenCalled();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Disconnect" }),
    );
    await waitFor(() =>
      expect(api.deleteLarkInstallation).toHaveBeenCalledWith("ws-1", "i1"),
    );
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "Disconnect" }),
      ).toBeEnabled(),
    );
    expect(dialog).toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Disconnect" }),
    );
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });
});

describe("shared connection dialog", () => {
  it("offers only Feishu for new connections while international Lark is disabled", async () => {
    show(
      <ChannelConnectDialog
        agents={[agent]}
        initialAgent={agent}
        initialChannel="lark"
        open
        onOpenChange={vi.fn()}
      />,
    );
    const feishu = await screen.findByRole("button", { name: "Feishu" });
    await waitFor(() => expect(feishu).toBeEnabled());
    expect(screen.queryByRole("button", { name: "Lark" })).toBeNull();
    expect(
      screen.queryByRole("option", { name: "Lark (International)" }),
    ).toBeNull();
    fireEvent.click(feishu);
    await waitFor(() =>
      expect(api.beginLarkInstall).toHaveBeenCalledWith(
        "ws-1",
        "agent-1",
        "feishu",
      ),
    );
  });

  it("starts the Feishu connection as soon as the platform is clicked", async () => {
    show(
      <ChannelConnectDialog
        agents={[agent]}
        initialAgent={agent}
        open
        onOpenChange={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    const feishu = await screen.findByRole("button", { name: "Feishu" });
    await waitFor(() => expect(feishu).toBeEnabled());
    fireEvent.click(feishu);
    await waitFor(() =>
      expect(api.beginLarkInstall).toHaveBeenCalledWith(
        "ws-1",
        "agent-1",
        "feishu",
      ),
    );
  });

  it("requires an agent selection when opened from the overview", async () => {
    show(
      <ChannelConnectDialog
        agents={[agent, otherAgent]}
        initialChannel="lark"
        open
        onOpenChange={vi.fn()}
      />,
    );
    const feishu = await screen.findByRole("button", { name: "Feishu" });
    expect(feishu).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Agent"), {
      target: { value: "agent-2" },
    });
    await waitFor(() => expect(feishu).toBeEnabled());
    fireEvent.click(feishu);
    await waitFor(() =>
      expect(api.beginLarkInstall).toHaveBeenCalledWith(
        "ws-1",
        "agent-2",
        "feishu",
      ),
    );
  });
});

describe("workspace overview", () => {
  it("lets admins disconnect an installation whose agent was deleted", async () => {
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([{ ...active, agent_id: "deleted-agent" }]) as never,
    );
    vi.mocked(api.deleteLarkInstallation).mockResolvedValue(undefined);
    show(<ChannelsTab />);
    expect(await screen.findByText("Agent unavailable")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(api.deleteLarkInstallation).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));
    await waitFor(() =>
      expect(api.deleteLarkInstallation).toHaveBeenCalledWith("ws-1", "i1"),
    );
  });

  it("links each connected agent to their channels, and redirects old detail links", async () => {
    state.search = "tab=channels&integration=slack";
    vi.mocked(api.listLarkInstallations).mockResolvedValue(
      listing([active]) as never,
    );
    show(<ChannelsTab />);
    expect(await screen.findByRole("link", { name: "Mika" })).toHaveAttribute(
      "href",
      "/acme/agents/agent-1?view=channels",
    );
    expect(state.replace).toHaveBeenCalledWith("/acme/settings?tab=channels");
    expect(screen.getByText("Who can connect")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Connect" })).toHaveLength(1);
  });

  it("reports failed reads as unavailable and keeps a retry control", async () => {
    vi.mocked(api.listLarkInstallations).mockRejectedValue(new Error("offline"));
    show(<ChannelsTab />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Status unavailable",
    );
    expect(
      screen.getByRole("button", { name: "Scan again" }),
    ).toBeInTheDocument();
  });
});
