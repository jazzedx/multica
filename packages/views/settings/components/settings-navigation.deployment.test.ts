// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { setDeploymentOffers } from "@multica/core/deployment";
import { CHANNEL_INTEGRATIONS, offeredChannels } from "./settings-navigation";

afterEach(() => setDeploymentOffers({}));

describe("offeredChannels", () => {
  it("offers every channel by default", () => {
    expect(offeredChannels()).toEqual([...CHANNEL_INTEGRATIONS]);
  });

  it("keeps only the channels the deployment offers, in the product's order", () => {
    setDeploymentOffers({ channels: ["telegram", "lark"] });
    expect(offeredChannels()).toEqual(["lark", "telegram"]);
  });
});
