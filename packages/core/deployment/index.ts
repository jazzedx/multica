/**
 * What this deployment offers its users.
 *
 * Every switch defaults to on, which is the product as built. An app that is
 * deployed without one of these turns it off once at startup, before anything
 * renders, and the UI leaves the matching entry points out. The code behind an
 * entry point stays in place either way.
 */
export interface DeploymentOffers {
  /**
   * Links and prompts that promote the public product: changelog, community,
   * GitHub star, release announcements, and the signup-source survey.
   */
  marketing: boolean;
  /** Prompts to download the desktop app or hand a page off to it. */
  desktopApp: boolean;
  /** Runtimes operated by a vendor, such as cloud agent services. */
  hostedRuntimes: boolean;
  /** Messaging channels a workspace can connect; null offers every channel. */
  channels: readonly string[] | null;
}

const EVERYTHING: DeploymentOffers = {
  marketing: true,
  desktopApp: true,
  hostedRuntimes: true,
  channels: null,
};

let offers = EVERYTHING;

/** Replaces the current offers; switches left out go back to on. */
export function setDeploymentOffers(next: Partial<DeploymentOffers>): void {
  offers = { ...EVERYTHING, ...next };
}

export function deploymentOffers(): DeploymentOffers {
  return offers;
}

export function offersChannel(channel: string): boolean {
  return offers.channels === null || offers.channels.includes(channel);
}
