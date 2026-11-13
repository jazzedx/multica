"use client";

import { useState } from "react";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";
import { docsLocalePrefix } from "../../common/docs-locale";
import { openExternal } from "../../platform";
import { IntegrationChannelIcon } from "./integration-channel-icon";
import {
  CHANNEL_INTEGRATIONS,
  type ChannelIntegration,
} from "./settings-navigation";
import { useMessagingChannels } from "./use-messaging-channels";
import { LarkChannelAuthorization } from "./lark-channel-authorization";

export function ChannelConnectDialog({
  agents,
  initialAgent,
  initialChannel,
  open,
  onOpenChange,
}: {
  agents: Agent[];
  initialAgent?: Agent;
  initialChannel?: ChannelIntegration;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("settings");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(($) => $.channels.connect_title)}</DialogTitle>
        </DialogHeader>
        {open ? (
          <ChannelConnectSteps
            agents={agents}
            initialAgent={initialAgent}
            initialChannel={initialChannel}
            onClose={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function ChannelConnectSteps({
  agents,
  initialAgent,
  initialChannel,
  onClose,
}: {
  agents: Agent[];
  initialAgent?: Agent;
  initialChannel?: ChannelIntegration;
  onClose: () => void;
}) {
  const { t, i18n } = useT("settings");
  const { wsId, queries, connections, isAdmin, canManage, available } =
    useMessagingChannels();
  const [agentId, setAgentId] = useState(initialAgent?.id ?? "");
  const [channel, setChannel] = useState<ChannelIntegration | undefined>(
    initialChannel,
  );
  const [authorizing, setAuthorizing] = useState(false);
  const agent =
    initialAgent ?? agents.find((candidate) => candidate.id === agentId);
  const connected = (id: ChannelIntegration) =>
    connections.some(
      (entry) =>
        entry.channel === id && entry.installation.agent_id === agent?.id,
    );
  // Picking a platform is the only choice left once the agent is known, so the
  // row itself starts the connection instead of waiting for a Continue click.
  const connect = (id: ChannelIntegration) => {
    setChannel(id);
    setAuthorizing(true);
  };

  return (
    <>
      <DialogDescription>
        {authorizing && agent && channel
          ? t(($) => $.channels.authorize_description, {
              agent: agent.name,
              platform: t(($) => $.lark.region_feishu),
            })
          : t(($) => $.channels.choose_description)}
      </DialogDescription>
      {!authorizing ? (
        <>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="channel-agent">
                {t(($) => $.channels.agent_label)}
              </Label>
              {initialAgent ? (
                <p className="text-body">{initialAgent.name}</p>
              ) : (
                <select
                  id="channel-agent"
                  className="h-9 w-full rounded-md border bg-background px-3 text-body"
                  value={agentId}
                  onChange={(event) => setAgentId(event.target.value)}
                >
                  <option value="">{t(($) => $.channels.choose_agent)}</option>
                  {agents
                    .filter(
                      (candidate) =>
                        !candidate.archived_at &&
                        (isAdmin ||
                          CHANNEL_INTEGRATIONS.some((id) =>
                            canManage(id, candidate),
                          )),
                    )
                    .map((candidate) => (
                      <option key={candidate.id} value={candidate.id}>
                        {candidate.name}
                      </option>
                    ))}
                </select>
              )}
            </div>
            <div
              className="space-y-1.5"
              role="group"
              aria-label={t(($) => $.channels.platform_label)}
            >
              <Label>{t(($) => $.channels.platform_label)}</Label>
              {CHANNEL_INTEGRATIONS.filter(
                (id) => queries[id].data?.configured || isAdmin,
              )
                .filter((id) => !connected(id))
                .map((id) => {
                  const permitted = !!agent && canManage(id, agent);
                  return (
                    <Button
                      key={id}
                      variant={channel === id ? "secondary" : "outline"}
                      className="w-full justify-start"
                      disabled={
                        !agent ||
                        !!agent.archived_at ||
                        !available(id) ||
                        !permitted
                      }
                      onClick={() => connect(id)}
                    >
                      <IntegrationChannelIcon channel={id} />
                      <span>{t(($) => $.lark.region_feishu)}</span>{" "}
                      <span className="ml-auto text-caption text-muted-foreground">
                        {queries[id].isError
                          ? t(($) => $.integrations.status_unknown)
                          : !available(id)
                            ? t(($) => $.channels.unavailable)
                            : agent && !permitted
                              ? t(($) => $.channels.ask_admin)
                              : null}
                      </span>
                    </Button>
                  );
                })}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              {t(($) => $.lark.install_close)}
            </Button>
          </DialogFooter>
        </>
      ) : !agent ||
        !channel ||
        !canManage(channel, agent) ||
        !available(channel) ? (
        <>
          <p role="alert" className="text-body text-muted-foreground">
            {t(($) => $.channels.connection_changed)}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              {t(($) => $.lark.install_close)}
            </Button>
          </DialogFooter>
        </>
      ) : (
        <>
          <Button
            variant="link"
            className="w-fit p-0"
            onClick={() =>
              openExternal(
                `https://multica.ai/docs${docsLocalePrefix(i18n.language)}/lark-bot-integration`,
              )
            }
          >
            {t(($) => $.channels.setup_guide)}
          </Button>
          <LarkChannelAuthorization
            wsId={wsId}
            agentId={agent.id}
            // International installs remain disabled until the persistence
            // issue is fixed; existing Lark bots stay manageable.
            region="feishu"
            onClose={onClose}
          />
        </>
      )}
    </>
  );
}
