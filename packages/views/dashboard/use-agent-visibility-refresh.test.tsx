import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { dashboardKeys } from "@multica/core/dashboard";
import { useAgentVisibilityRefresh } from "./use-agent-visibility-refresh";

function setup(initial: { wsId: string; agentIds: string[] | undefined }) {
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const view = renderHook(({ wsId, agentIds }) => useAgentVisibilityRefresh(wsId, agentIds), {
    wrapper,
    initialProps: initial,
  });
  return { invalidate, rerender: view.rerender };
}

describe("useAgentVisibilityRefresh", () => {
  it("refetches the dashboard when an agent leaves or joins the viewer's list", () => {
    const { invalidate, rerender } = setup({ wsId: "ws-1", agentIds: ["ada", "turing"] });
    expect(invalidate).not.toHaveBeenCalled();

    // Turing was made private to someone else.
    rerender({ wsId: "ws-1", agentIds: ["ada"] });
    expect(invalidate).toHaveBeenCalledTimes(1);
    expect(invalidate).toHaveBeenLastCalledWith({ queryKey: dashboardKeys.all("ws-1") });

    // Kepler was shared with the viewer.
    rerender({ wsId: "ws-1", agentIds: ["ada", "kepler"] });
    expect(invalidate).toHaveBeenCalledTimes(2);
  });

  it("takes the first loaded list as the baseline and ignores a reordered one", () => {
    const { invalidate, rerender } = setup({ wsId: "ws-1", agentIds: undefined });

    rerender({ wsId: "ws-1", agentIds: ["ada", "turing"] });
    rerender({ wsId: "ws-1", agentIds: ["turing", "ada"] });
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("does not read another workspace's agents as a change", () => {
    const { invalidate, rerender } = setup({ wsId: "ws-1", agentIds: ["ada"] });

    rerender({ wsId: "ws-2", agentIds: ["kepler"] });
    expect(invalidate).not.toHaveBeenCalled();
  });
});
