// @vitest-environment node

import { describe, expect, it } from "vitest";
import { daemonCommands } from "@multica/core/runtimes";
import { buildInstallRuntimeIssueBody } from "./install-runtime-issue";

const selfHost = daemonCommands({
  status: "ready",
  target: "self_host",
  serverUrl: "http://10.0.0.5:8080/",
  appUrl: "http://10.0.0.5:3000",
});

describe("buildInstallRuntimeIssueBody", () => {
  it("gives this deployment's own install and connect commands", () => {
    if (selfHost.status !== "ready") throw new Error("fixture must be ready");
    const body = buildInstallRuntimeIssueBody("zh", selfHost);
    expect(body).toContain(selfHost.installCmds.macosLinux);
    expect(body).toContain(selfHost.installCmds.windows);
    expect(body).toContain(selfHost.setupCmd);
  });

  it("leaves the commands out while they are not known", () => {
    const body = buildInstallRuntimeIssueBody("zh", { status: "loading" });
    expect(body).not.toContain("multica setup");
  });

  it("gives languages without an authored guide the English one", () => {
    const en = buildInstallRuntimeIssueBody("en", selfHost);
    expect(buildInstallRuntimeIssueBody("ko", selfHost)).toBe(en);
    expect(buildInstallRuntimeIssueBody("ja", selfHost)).toBe(en);
  });
});
