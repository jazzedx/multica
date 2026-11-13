"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import { larkInstallationsOptions } from "@multica/core/lark";
import type { Agent, LarkInstallation } from "@multica/core/types";
import type { ChannelIntegration } from "./settings-navigation";

export type ChannelConnection = {
  channel: "lark";
  installation: LarkInstallation;
};

export function channelChatLink(
  connection: ChannelConnection,
): string | undefined {
  return connection.installation.app_id
    ? `https://${connection.installation.region === "lark" ? "applink.larksuite.com" : "applink.feishu.cn"}/client/bot/open?appId=${encodeURIComponent(connection.installation.app_id)}`
    : undefined;
}

export function useMessagingChannels() {
  const wsId = useWorkspaceId();
  const { member, userId, role } = useCurrentMember(wsId);
  const enabled = !!wsId && !!member;
  const lark = useQuery({ ...larkInstallationsOptions(wsId), enabled });
  const queries = { lark };
  const connections: ChannelConnection[] = (lark.data?.installations ?? [])
    .filter((installation) => installation.status === "active")
    .map((installation) => ({
      channel: "lark" as const,
      installation,
    }));
  const isAdmin = role === "owner" || role === "admin";
  const canManage = (channel: ChannelIntegration, agent: Agent) =>
    !!member && (isAdmin || (channel === "lark" && agent.owner_id === userId));
  const available = (channel: ChannelIntegration) =>
    queries[channel].data?.configured === true &&
    queries[channel].data?.install_supported === true;

  return { wsId, queries, connections, isAdmin, canManage, available };
}
