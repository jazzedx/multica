// @vitest-environment node
import { describe, expect, it } from "vitest";
import { deploymentOffers } from "@multica/core/deployment";
import "./deployment";

describe("the web app's deployment", () => {
  it("is private: no marketing, desktop app or hosted runtimes, and Feishu only", () => {
    expect(deploymentOffers()).toEqual({
      marketing: false,
      desktopApp: false,
      hostedRuntimes: false,
      channels: ["lark"],
    });
  });
});
