export const CLOUD_SERVER_URL = "https://api.multica.ai";
export const CLOUD_APP_URL = "https://multica.ai";

export function normalizeCommandURL(url: string | undefined): string {
  return url?.trim().replace(/\/+$/, "") ?? "";
}

export type DaemonSetupTarget = "cloud" | "self_host" | "unknown";
export type DaemonConfigStatus = "loading" | "ready" | "error";

export interface DaemonSetupConfig {
  status: DaemonConfigStatus;
  target: DaemonSetupTarget;
  serverUrl?: string;
  appUrl?: string;
}

/** The CLI install command, one per platform family. */
export interface CliInstallCommands {
  macosLinux: string;
  windows: string;
}

// The managed Cloud installs the published release from the public repository.
const CLOUD_CLI_INSTALL_COMMANDS: CliInstallCommands = {
  macosLinux:
    "curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash",
  windows:
    "irm https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.ps1 | iex",
};

/**
 * A self-hosted server hands out the CLI from its own build, so a machine
 * needs no route to the public internet and its CLI matches the server. The
 * installer cannot tell which URL it was fetched from once piped into a shell,
 * so the command passes the server URL to it.
 */
function selfHostCliInstallCommands(serverUrl: string): CliInstallCommands {
  return {
    macosLinux: `curl -fsSL ${serverUrl}/api/cli/install.sh | bash -s -- ${serverUrl}`,
    windows: `$env:MULTICA_SERVER_URL="${serverUrl}"; irm ${serverUrl}/api/cli/install.ps1 | iex`,
  };
}

export type DaemonCommands =
  | {
      status: "ready";
      installCmds: CliInstallCommands;
      setupCmd: string;
      tokenCmd: string;
    }
  | { status: "loading" | "error" | "unknown" | "incomplete" };

export function daemonCommands(config: DaemonSetupConfig): DaemonCommands {
  if (config.status !== "ready") return { status: config.status };

  if (config.target === "self_host") {
    const normalizedServerUrl = normalizeCommandURL(config.serverUrl);
    const normalizedAppUrl = normalizeCommandURL(config.appUrl);
    if (!normalizedServerUrl || !normalizedAppUrl) return { status: "incomplete" };
    return {
      status: "ready",
      installCmds: selfHostCliInstallCommands(normalizedServerUrl),
      setupCmd: `multica setup self-host --server-url ${normalizedServerUrl} --app-url ${normalizedAppUrl}`,
      tokenCmd: `multica config set server_url ${normalizedServerUrl}
multica config set app_url ${normalizedAppUrl}
multica login --token <YOUR_TOKEN>
multica daemon start`,
    };
  }

  // Empty URLs are also returned by misconfigured self-hosted servers.
  if (config.target !== "cloud") return { status: "unknown" };
  return {
    status: "ready",
    installCmds: CLOUD_CLI_INSTALL_COMMANDS,
    setupCmd: "multica setup",
    tokenCmd: `multica config set server_url ${CLOUD_SERVER_URL}
multica config set app_url ${CLOUD_APP_URL}
multica login --token <YOUR_TOKEN>
multica daemon start`,
  };
}
