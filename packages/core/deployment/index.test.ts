// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { deploymentOffers, offersChannel, setDeploymentOffers } from "./index";

afterEach(() => setDeploymentOffers({}));

describe("deployment offers", () => {
  it("offers everything until an app turns something off", () => {
    expect(deploymentOffers()).toEqual({
      marketing: true,
      desktopApp: true,
      hostedRuntimes: true,
      channels: null,
    });
    expect(offersChannel("slack")).toBe(true);
  });

  it("turns off only what it is told to, and resets the rest", () => {
    setDeploymentOffers({ desktopApp: false, channels: ["lark"] });
    expect(deploymentOffers().desktopApp).toBe(false);
    expect(deploymentOffers().marketing).toBe(true);
    expect(offersChannel("lark")).toBe(true);
    expect(offersChannel("slack")).toBe(false);

    setDeploymentOffers({ marketing: false });
    expect(deploymentOffers().desktopApp).toBe(true);
    expect(offersChannel("slack")).toBe(true);
  });
});
