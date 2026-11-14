"use client";

import { useQuery } from "@tanstack/react-query";
import { chatSessionTaskRunsOptions } from "@multica/core/chat/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import { TranscriptButton } from "../../common/task-transcript";
import { useT } from "../../i18n";

/**
 * Opens the full transcript of the run behind one chat reply: the same dialog
 * an issue's execution log opens, with the run's timing and what it cost.
 *
 * The chat already shows the steps inline; this is for reading a run closely.
 * Renders nothing until the session's run history has the run — a reply whose
 * run is gone has no transcript to open.
 */
export function ChatRunTranscriptButton({
  sessionId,
  taskId,
}: {
  sessionId: string;
  taskId: string | null | undefined;
}) {
  const { t } = useT("chat");
  const { data: runs, isError } = useQuery({
    ...chatSessionTaskRunsOptions(sessionId),
    enabled: !!sessionId && !!taskId,
  });
  // A failed refresh keeps the last result in the cache, but the server may be
  // refusing because the viewer can no longer read this chat; a transcript it
  // would not serve is not offered.
  const run = taskId && !isError ? runs?.find((candidate) => candidate.id === taskId) : undefined;
  // Only for the dialog's heading; the transcript opens without it. The run
  // names its workspace, so this needs no workspace context of its own.
  const wsId = run?.workspace_id ?? "";
  const { data: agents = [] } = useQuery({ ...agentListOptions(wsId), enabled: !!wsId });

  if (!run) return null;

  return (
    <TranscriptButton
      task={run}
      agentName={agents.find((agent) => agent.id === run.agent_id)?.name ?? ""}
      title={t(($) => $.message_list.transcript_action)}
      className="text-faint-foreground hover:text-foreground"
    />
  );
}
