// @vitest-environment node
import { describe, expect, it } from "vitest";
import { channelChatLink, type ChannelConnection } from "./use-messaging-channels";

describe("channel chat destinations", () => {
  it.each([
    ["feishu", "applink.feishu.cn"],
    ["lark", "applink.larksuite.com"],
    [undefined, "applink.feishu.cn"],
  ])("opens the %s bot conversation with the application id", (region, host) => {
    expect(channelChatLink({ channel: "lark", installation: { app_id: "cli_a&b", region } } as ChannelConnection))
      .toBe(`https://${host}/client/bot/open?appId=cli_a%26b`);
  });

  it("does not offer a broken link when a bot identity is missing", () => {
    expect(channelChatLink({ channel: "lark", installation: {} } as ChannelConnection)).toBeUndefined();
  });
});
