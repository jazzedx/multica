"use client";

import type { ReactNode } from "react";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import type { CliInstallCommands } from "@multica/core/runtimes";

export type CliInstallPlatform = keyof CliInstallCommands;

/** Tab order. macOS / Linux stays first so the default matches the old single command. */
const PLATFORMS = [
  "macosLinux",
  "windows",
] as const satisfies readonly CliInstallPlatform[];

export interface CliInstallCommandLabels {
  /** Accessible name for the platform switch. */
  group: string;
  macosLinux: string;
  windows: string;
}

/**
 * Platform switch for the install command, built on the shared Tabs primitive
 * so tab semantics and arrow-key navigation come for free. The commands come
 * from `useDaemonSetupCommands`, which knows which server the CLI installs from.
 *
 * The switch is the only shared UI: `children` receives the selected command
 * and renders it, so each surface keeps its own command row — the product
 * surfaces their token-styled row with an icon copy button, the landing page
 * its marketing palette with a spelled-out one.
 */
export function CliInstallCommand({
  commands,
  labels,
  classNames,
  children,
}: {
  commands: CliInstallCommands;
  labels: CliInstallCommandLabels;
  /** Palette overrides for surfaces that do not use the product tokens. */
  classNames?: { list?: string; trigger?: string };
  children: (command: string) => ReactNode;
}) {
  return (
    <Tabs defaultValue={PLATFORMS[0]}>
      <TabsList
        aria-label={labels.group}
        activateOnFocus
        className={classNames?.list}
      >
        {PLATFORMS.map((platform) => (
          <TabsTrigger
            key={platform}
            value={platform}
            className={classNames?.trigger}
          >
            {labels[platform]}
          </TabsTrigger>
        ))}
      </TabsList>
      {PLATFORMS.map((platform) => (
        <TabsContent key={platform} value={platform}>
          {children(commands[platform])}
        </TabsContent>
      ))}
    </Tabs>
  );
}
