import { LarkMark } from "./lark-mark";
import type { ChannelIntegration } from "./settings-navigation";

// Feishu / Lark is the only messaging channel. A new channel needs its own
// `*-mark.tsx` before it can be listed here.
export function IntegrationChannelIcon({
  channel,
}: {
  channel: ChannelIntegration;
}) {
  return (
    <span
      aria-hidden="true"
      data-testid={`integration-channel-icon-${channel}`}
      className="flex size-5 shrink-0 items-center justify-center text-muted-foreground"
    >
      <LarkMark className="h-4 w-4" />
    </span>
  );
}
