// @vitest-environment jsdom

import { cleanup, fireEvent, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildIssueStatusCatalog } from "@multica/core/issue-statuses";
import { agentListOptions, memberListOptions, squadListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import type { IssueWorkflow, IssueWorkflowStep } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { HandoffFields, WorkflowHandoffForm, savedHandoff } from "./handoff-form";

const { mutateAsync } = vi.hoisted(() => ({ mutateAsync: vi.fn() }));

vi.mock("@multica/core/api", () => ({ api: {}, clientErrorMessage: () => null }));
vi.mock("@multica/core/issue-workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/issue-workflows")>()),
  useSetWorkflowHandoff: () => ({ mutateAsync, isPending: false }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/issue-statuses/hooks", () => ({ useIssueStatuses: () => buildIssueStatusCatalog([]) }));
vi.mock("../common/actor-avatar", () => ({ ActorAvatar: () => null }));

const steps = (...keys: string[]): IssueWorkflowStep[] =>
  keys.map((status_key) => ({ status_key, handler: { type: "none" as const }, instructions: "" }));

const REVIEW: IssueWorkflowStep = {
  status_key: "in_review",
  handler: { type: "agent", id: "agent-1" },
  instructions: "Review it.",
  back_status_key: "in_progress",
};

function client() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(agentListOptions("ws-1").queryKey, [{ id: "agent-1", name: "Reviewer", archived_at: null }] as never);
  qc.setQueryData(memberListOptions("ws-1").queryKey, [] as never);
  qc.setQueryData(squadListOptions("ws-1").queryKey, [] as never);
  qc.setQueryData(projectListOptions("ws-1").queryKey, { projects: [] } as never);
  return qc;
}

function renderFields(step: IssueWorkflowStep, workflowSteps: IssueWorkflowStep[], onPatch = vi.fn()) {
  renderWithI18n(
    <QueryClientProvider client={client()}>
      <HandoffFields step={step} workflow={{ steps: workflowSteps }} canEdit onPatch={onPatch} />
    </QueryClientProvider>,
  );
  return onPatch;
}

afterEach(cleanup);

describe("step rules in the handoff form", () => {
  const WITH_BLOCKED = steps("todo", "in_progress", "in_review", "blocked", "done");

  it("turns the step's moves into the only ones a run may make", () => {
    const onPatch = renderFields(REVIEW, WITH_BLOCKED);

    fireEvent.click(screen.getByRole("checkbox", { name: /Runs can only make these moves/ }));

    expect(onPatch).toHaveBeenCalledWith({ strict: true });
    expect(screen.getByText(/People are not limited/)).toBeTruthy();
  });

  it("offers a cap on sending back only where there is a way back", () => {
    renderFields({ ...REVIEW, back_status_key: undefined }, WITH_BLOCKED);
    expect(screen.queryByRole("combobox", { name: "Send back at most" })).toBeNull();

    cleanup();
    renderFields({ ...REVIEW, max_returns: 2 }, WITH_BLOCKED);
    const cap = screen.getByRole("combobox", { name: "Send back at most" });
    expect(cap.textContent).toContain("2 times");
    expect(screen.getByText(/has to set the issue to blocked/)).toBeTruthy();
  });

  // A run at the cap has to stop somewhere that gets attention.
  it("asks for Blocked before a cap can be set", () => {
    renderFields(REVIEW, steps("todo", "in_progress", "in_review", "done"));

    const cap = screen.getByRole("combobox", { name: "Send back at most" });
    expect(cap.hasAttribute("disabled") || cap.getAttribute("aria-disabled") === "true" || cap.hasAttribute("data-disabled")).toBe(true);
    expect(screen.getByText(/Add Blocked to this workflow/)).toBeTruthy();
  });

  it("saves the rules with the handoff", async () => {
    const workflow = {
      id: "wf-1",
      workspace_id: "ws-1",
      name: "Delivery",
      description: "",
      is_default: false,
      steps: WITH_BLOCKED.map((step) => (step.status_key === "in_review" ? { ...REVIEW, max_returns: 2 } : step)),
      project_ids: [],
      created_at: "",
      updated_at: "",
    } as unknown as IssueWorkflow;
    mutateAsync.mockReset().mockResolvedValue(workflow);
    renderWithI18n(
      <QueryClientProvider client={client()}>
        <WorkflowHandoffForm workflow={workflow} statusKey="in_review" canEdit canCopyForProject={false} />
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByRole("checkbox", { name: /Runs can only make these moves/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await vi.waitFor(() => expect(mutateAsync).toHaveBeenCalled());
    expect(mutateAsync.mock.calls[0]![0]).toMatchObject({
      statusKey: "in_review",
      back_status_key: "in_progress",
      strict: true,
      max_returns: 2,
    });
  });
});

describe("savedHandoff", () => {
  it("keeps the rules of an agent's step", () => {
    expect(savedHandoff({ ...REVIEW, strict: true, max_returns: 3 })).toMatchObject({ strict: true, max_returns: 3 });
  });

  it("drops a cap that has lost its way back", () => {
    const saved = savedHandoff({ ...REVIEW, back_status_key: undefined, strict: true, max_returns: 3 });
    expect(saved.strict).toBe(true);
    expect(saved.max_returns).toBeUndefined();
  });

  // The rules bind runs, and a member's step has none.
  it("drops the rules of a step handed to a member", () => {
    const saved = savedHandoff({ ...REVIEW, handler: { type: "member", id: "user-1" }, strict: true, max_returns: 3 });
    expect(saved.strict).toBeUndefined();
    expect(saved.max_returns).toBeUndefined();
  });
});
