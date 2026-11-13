"use client";

import { useEffect, useRef, useState } from "react";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { ExternalLink, MoreHorizontal, Plus } from "@multica/ui/icons";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";

import { docsLocalePrefix } from "../../common/docs-locale";
import { ObjectSection, ObjectSectionAction } from "../../layout/object-page";
import { useT } from "../../i18n";
import { openExternal } from "../../platform";
import { useNavigation } from "../../navigation";
import { IntegrationChannelIcon } from "../../settings/components/integration-channel-icon";
import { ChannelDisconnectDialog } from "../../settings/components/channel-disconnect-dialog";
import { ChannelConnectDialog } from "../../settings/components/channel-connect-dialog";
import { CHANNEL_INTEGRATIONS } from "../../settings/components/settings-navigation";
import {
  channelChatLink,
  useMessagingChannels,
  type ChannelConnection,
} from "../../settings/components/use-messaging-channels";

export function AgentChannelLinks({ agent }: { agent: Agent }) {
  const { connections } = useMessagingChannels();
  const links = connections.filter(
    (entry) => entry.installation.agent_id === agent.id,
  );
  if (links.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {links.map((entry) => (
        <AgentChannelContact key={entry.installation.id} connection={entry} />
      ))}
    </div>
  );
}

function AgentChannelContact({
  connection,
}: {
  connection: ChannelConnection;
}) {
  const { t } = useT("settings");
  const href = channelChatLink(connection);
  if (!href) return null;
  return (
    <Button variant="outline" size="sm" onClick={() => openExternal(href)}>
      <IntegrationChannelIcon channel={connection.channel} />
      {t(($) => $.channels.find_me, {
        platform: t(($) => $[connection.channel].section_title),
      })}
    </Button>
  );
}

export function AgentChannelsSection({ agent }: { agent: Agent }) {
  const { t } = useT("settings");
  const { queries, connections, canManage, available } =
    useMessagingChannels();
  const [open, setOpen] = useState(false);
  const sectionRef = useRef<HTMLDivElement>(null);
  const { searchParams } = useNavigation();
  const view = searchParams.get("view") ?? searchParams.get("tab");
  const installed = connections.filter(
    (entry) => entry.installation.agent_id === agent.id,
  );
  const canAdd =
    !agent.archived_at &&
    CHANNEL_INTEGRATIONS.some(
      (id) =>
        canManage(id, agent) &&
        available(id) &&
        !installed.some((entry) => entry.channel === id),
    );
  const failed = CHANNEL_INTEGRATIONS.filter((id) => queries[id].isError);
  const visible = installed.length > 0 || canAdd || failed.length > 0;
  useEffect(() => {
    if (visible && (view === "channels" || view === "integrations"))
      sectionRef.current?.scrollIntoView({ block: "start" });
  }, [visible, view]);
  if (!visible) return null;

  return (
    <div
      ref={sectionRef}
      id="messaging-channels"
      data-settings-anchor="channels"
    >
      <ObjectSection
        title={t(($) => $.page.tabs.channels)}
        count={installed.length}
        action={
          canAdd ? (
            <ObjectSectionAction icon={Plus} onClick={() => setOpen(true)}>
              {t(($) => $.channels.add)}
            </ObjectSectionAction>
          ) : undefined
        }
      >
        {installed.length ? (
          <div className="divide-y rounded-lg border">
            {installed.map((connection) => (
              <AgentChannelRow
                key={connection.installation.id}
                connection={connection}
                canManage={canManage(connection.channel, agent)}
              />
            ))}
          </div>
        ) : canAdd ? (
          <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
            <Plus />
            {t(($) => $.channels.add)}
          </Button>
        ) : null}
        {failed.map((id) => (
          <div
            key={id}
            role="alert"
            className="mt-2 flex items-center justify-between gap-3 text-caption text-muted-foreground"
          >
            <span>
              {t(($) => $[id].section_title)} ·{" "}
              {t(($) => $.integrations.status_unknown)}
            </span>
            <Button
              size="xs"
              variant="outline"
              onClick={() => void queries[id].refetch()}
            >
              {t(($) => $.lark.install_retry)}
            </Button>
          </div>
        ))}
      </ObjectSection>
      <ChannelConnectDialog
        agents={[agent]}
        initialAgent={agent}
        open={open}
        onOpenChange={setOpen}
      />
    </div>
  );
}

function AgentChannelRow({
  connection,
  canManage,
}: {
  connection: ChannelConnection;
  canManage: boolean;
}) {
  const { t, i18n } = useT("settings");
  const { channel, installation } = connection;
  const [confirm, setConfirm] = useState(false);
  const pending = !installation.first_message_received_at;
  const href = channelChatLink(connection);
  const larkManageHref = installation.app_id
    ? `https://${installation.region === "lark" ? "open.larksuite.com" : "open.feishu.cn"}/app/${encodeURIComponent(installation.app_id)}`
    : undefined;

  return (
    <div className="px-3 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <IntegrationChannelIcon channel={channel} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="text-body font-medium">
              {t(($) => $[channel].section_title)}
            </span>
          </div>
          <span
            className={
              pending
                ? "text-caption text-muted-foreground"
                : "text-caption text-success"
            }
          >
            {pending
              ? t(($) => $.channels.waiting_message)
              : t(($) => $.channels.normal)}
          </span>
        </div>
        {href ? (
          <Button size="xs" variant="ghost" onClick={() => openExternal(href)}>
            <ExternalLink />
            {t(($) => $.channels.open)}
          </Button>
        ) : null}
        {canManage ? (
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <IconButton
                  label={t(($) => $.channels.manage, {
                    platform: t(($) => $[channel].section_title),
                  })}
                  size="icon-xs"
                >
                  <MoreHorizontal />
                </IconButton>
              }
            />
            <DropdownMenuContent align="end">
              {larkManageHref ? (
                <DropdownMenuItem
                  onClick={() => openExternal(larkManageHref)}
                >
                  <ExternalLink />
                  {installation.region === "lark"
                    ? t(($) => $.lark.agent_bot_manage_link_lark)
                    : t(($) => $.lark.agent_bot_manage_link_feishu)}
                </DropdownMenuItem>
              ) : null}
              <DropdownMenuItem
                onClick={() =>
                  openExternal(
                    `https://multica.ai/docs${docsLocalePrefix(i18n.language)}/lark-bot-integration`,
                  )
                }
              >
                <ExternalLink />
                {t(($) => $.lark.agent_bot_silent_hint_link)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onClick={() => setConfirm(true)}
              >
                {t(($) => $.channels.disconnect)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}
      </div>
      <ChannelDisconnectDialog
        connection={connection}
        open={confirm}
        onOpenChange={setConfirm}
      />
    </div>
  );
}
