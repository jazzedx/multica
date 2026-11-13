import { setDeploymentOffers } from "@multica/core/deployment";

// This deployment runs inside one organization, in the browser, on machines
// the organization manages, and talks to its users through Feishu. Imported
// for its side effect by the root providers, so it runs before any page
// renders.
setDeploymentOffers({
  marketing: false,
  desktopApp: false,
  hostedRuntimes: false,
  channels: ["lark"],
});
