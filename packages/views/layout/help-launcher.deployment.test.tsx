import { cloneElement, type ReactElement, type ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setDeploymentOffers } from "@multica/core/deployment";
import enLayout from "../locales/en/layout.json";
import { HelpLauncher } from "./help-launcher";

vi.mock("../platform/local-directory", () => ({ isDesktopShell: () => false }));

vi.mock("../whats-new/whats-new-menu-panel", () => ({
  useWhatsNewMenuPanel: () => ({ available: false }),
  WhatsNewMenuPanel: () => null,
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    i18n: { language: "en" },
    t: (sel: (r: typeof enLayout) => string) => sel(enLayout),
  }),
}));

// Flattened so the menu content is always in the DOM; `render` keeps the
// caller's <a>, which is what the assertions look for.
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children, render: slot }: { children: ReactNode; render?: ReactElement }) =>
    slot ? cloneElement(slot, {}, children) : <>{children}</>,
  DropdownMenuGroup: ({ children }: { children: ReactNode }) => <>{children}</>,
  DropdownMenuLabel: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => null,
  DropdownMenuTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

function renderLauncher() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <HelpLauncher />
    </QueryClientProvider>,
  );
}

afterEach(() => setDeploymentOffers({}));

describe("HelpLauncher in a deployment that turns entries off", () => {
  it("leaves out the desktop download when the desktop app is not offered", () => {
    setDeploymentOffers({ desktopApp: false });
    renderLauncher();
    expect(screen.queryByRole("link", { name: /Desktop app/ })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Changelog/ })).toBeInTheDocument();
  });

  it("leaves out changelog, community and star links without marketing, and keeps docs", () => {
    setDeploymentOffers({ marketing: false });
    renderLauncher();
    expect(screen.queryByRole("link", { name: /Changelog/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Discord/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /GitHub/ })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Documentation/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Desktop app/ })).toBeInTheDocument();
  });
});
