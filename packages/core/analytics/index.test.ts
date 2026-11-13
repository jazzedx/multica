import { describe, expect, it } from "vitest";
import {
  captureEvent,
  captureException,
  captureSignupSource,
  detectClientType,
  identify,
  initAnalytics,
  resetAnalytics,
  setPersonProperties,
} from "./index";

describe("analytics stubs", () => {
  it("does not initialize", () => {
    expect(initAnalytics({ key: "k", host: "https://example.test" })).toBe(false);
  });

  it("exposes no-op helpers", () => {
    expect(detectClientType()).toBe("web");
    identify("user-1");
    resetAnalytics();
    setPersonProperties({ role: "admin" });
    captureException(new Error("x"));
    captureSignupSource();
    let captured = false;
    captureEvent("noop", undefined, { onCaptured: () => {
      captured = true;
    } });
    expect(captured).toBe(true);
  });
});
