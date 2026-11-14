package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

var dependencyListenersOnce sync.Once

// dependencyTestSetup wires the release listener the server registers at
// startup; the shared test handler is built without it.
func dependencyTestSetup(t *testing.T) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	dependencyListenersOnce.Do(testHandler.RegisterIssueDependencyListeners)
}

func createDependencyTestIssue(t *testing.T, body map[string]any) IssueResponse {
	t.Helper()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, body)
	w := testutil.Call(t, testHandler.CreateIssue, req).Want(http.StatusCreated)
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue_dependency WHERE issue_id = $1 OR depends_on_issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})
	return created
}

func setDependencyTestIssueStatus(t *testing.T, issueID, status string) {
	t.Helper()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{"status": status})
	req = withURLParam(req, "id", issueID)
	testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusOK)
}

func queuedDependencyTestRuns(t *testing.T, issueID, agentID string) int {
	t.Helper()
	var runs int
	dbfx.QueryRow(t,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		issueID, agentID,
	).Scan(&runs)
	return runs
}

func TestIssueCreatedWaitingStartsWhenItsBlockerIsDone(t *testing.T) {
	dependencyTestSetup(t)
	agentID := createHandlerTestAgent(t, "Dependency Release Agent", nil)

	blocker := createDependencyTestIssue(t, map[string]any{"title": "Upstream work"})
	waiting := createDependencyTestIssue(t, map[string]any{
		"title":         "Downstream work",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"blocked_by":    []string{blocker.ID},
	})
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 0 {
		t.Fatalf("an issue created waiting must not start, got %d queued runs", runs)
	}

	setDependencyTestIssueStatus(t, blocker.ID, "in_progress")
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 0 {
		t.Fatalf("a blocker that is only started must not release, got %d queued runs", runs)
	}

	setDependencyTestIssueStatus(t, blocker.ID, "done")
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 1 {
		t.Fatalf("a done blocker must start the waiting issue once, got %d queued runs", runs)
	}
}

func TestIssueWaitsForEveryBlocker(t *testing.T) {
	dependencyTestSetup(t)
	agentID := createHandlerTestAgent(t, "Dependency Join Agent", nil)

	first := createDependencyTestIssue(t, map[string]any{"title": "Branch one"})
	second := createDependencyTestIssue(t, map[string]any{"title": "Branch two"})
	// One blocker by id and one by identifier: both forms are accepted.
	join := createDependencyTestIssue(t, map[string]any{
		"title":         "Join",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"blocked_by":    []string{first.ID, second.Identifier},
	})

	setDependencyTestIssueStatus(t, first.ID, "done")
	if runs := queuedDependencyTestRuns(t, join.ID, agentID); runs != 0 {
		t.Fatalf("one of two blockers done must not release, got %d queued runs", runs)
	}
	setDependencyTestIssueStatus(t, second.ID, "done")
	if runs := queuedDependencyTestRuns(t, join.ID, agentID); runs != 1 {
		t.Fatalf("the last blocker done must release, got %d queued runs", runs)
	}
}

func TestAssigningWaitingIssueHoldsUntilReleased(t *testing.T) {
	dependencyTestSetup(t)
	agentID := createHandlerTestAgent(t, "Dependency Assign Agent", nil)

	blocker := createDependencyTestIssue(t, map[string]any{"title": "Upstream work"})
	waiting := createDependencyTestIssue(t, map[string]any{
		"title":      "Assigned later",
		"blocked_by": []string{blocker.ID},
	})

	req := newRequest("PUT", "/api/issues/"+waiting.ID, map[string]any{"assignee_type": "agent", "assignee_id": agentID})
	req = withURLParam(req, "id", waiting.ID)
	testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusOK)
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 0 {
		t.Fatalf("assigning a waiting issue must not start it, got %d queued runs", runs)
	}

	setDependencyTestIssueStatus(t, blocker.ID, "done")
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 1 {
		t.Fatalf("release must start the assigned issue, got %d queued runs", runs)
	}
}

func TestCancelledBlockerHoldsUntilTheDependencyIsRemoved(t *testing.T) {
	dependencyTestSetup(t)
	agentID := createHandlerTestAgent(t, "Dependency Cancel Agent", nil)

	blocker := createDependencyTestIssue(t, map[string]any{"title": "Upstream work"})
	waiting := createDependencyTestIssue(t, map[string]any{
		"title":         "Downstream work",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"blocked_by":    []string{blocker.ID},
	})

	setDependencyTestIssueStatus(t, blocker.ID, "cancelled")
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 0 {
		t.Fatalf("a cancelled blocker delivered nothing and must not release, got %d queued runs", runs)
	}

	req := newRequest("DELETE", "/api/issues/"+waiting.ID+"/dependencies/"+blocker.ID, nil)
	req = withURLParams(req, "id", waiting.ID, "blockerId", blocker.ID)
	w := testutil.Call(t, testHandler.RemoveIssueDependency, req).Want(http.StatusOK)
	var deps IssueDependenciesResponse
	json.NewDecoder(w.Body).Decode(&deps)
	if len(deps.BlockedBy) != 0 {
		t.Fatalf("the dependency must be gone, got %+v", deps.BlockedBy)
	}
	if runs := queuedDependencyTestRuns(t, waiting.ID, agentID); runs != 1 {
		t.Fatalf("removing the last dependency must start the issue, got %d queued runs", runs)
	}
}

func TestReleasedIssueInBacklogStaysParked(t *testing.T) {
	dependencyTestSetup(t)
	agentID := createHandlerTestAgent(t, "Dependency Backlog Agent", nil)

	blocker := createDependencyTestIssue(t, map[string]any{"title": "Upstream work"})
	parked := createDependencyTestIssue(t, map[string]any{
		"title":         "Parked work",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"blocked_by":    []string{blocker.ID},
	})

	setDependencyTestIssueStatus(t, blocker.ID, "done")
	if runs := queuedDependencyTestRuns(t, parked.ID, agentID); runs != 0 {
		t.Fatalf("backlog parks work whatever it waited for, got %d queued runs", runs)
	}
	// Leaving backlog is the ordinary promotion, and nothing blocks it now.
	setDependencyTestIssueStatus(t, parked.ID, "todo")
	if runs := queuedDependencyTestRuns(t, parked.ID, agentID); runs != 1 {
		t.Fatalf("promotion after release must start the issue, got %d queued runs", runs)
	}
}

func TestAddIssueDependencyRejectsSelfAndCycles(t *testing.T) {
	dependencyTestSetup(t)

	a := createDependencyTestIssue(t, map[string]any{"title": "A"})
	b := createDependencyTestIssue(t, map[string]any{"title": "B"})
	c := createDependencyTestIssue(t, map[string]any{"title": "C"})

	add := func(issueID, blockerRef string, want int) IssueDependenciesResponse {
		t.Helper()
		req := newRequest("POST", "/api/issues/"+issueID+"/dependencies", map[string]any{"blocked_by": blockerRef})
		req = withURLParam(req, "id", issueID)
		w := testutil.Call(t, testHandler.AddIssueDependency, req).Want(want)
		var deps IssueDependenciesResponse
		json.NewDecoder(w.Body).Decode(&deps)
		return deps
	}

	add(a.ID, a.ID, http.StatusBadRequest)
	// B waits for A, C waits for B; adding it twice changes nothing.
	add(b.ID, a.Identifier, http.StatusOK)
	deps := add(b.ID, a.ID, http.StatusOK)
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].ID != a.ID || deps.BlockedBy[0].Identifier != a.Identifier || deps.BlockedBy[0].Done {
		t.Fatalf("B must wait for A exactly once, got %+v", deps.BlockedBy)
	}
	add(c.ID, b.ID, http.StatusOK)
	// A waiting for C would close A -> C -> B -> A.
	add(a.ID, c.ID, http.StatusConflict)

	req := newRequest("GET", "/api/issues/"+b.ID+"/dependencies", nil)
	req = withURLParam(req, "id", b.ID)
	w := testutil.Call(t, testHandler.ListIssueDependencies, req).Want(http.StatusOK)
	json.NewDecoder(w.Body).Decode(&deps)
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].ID != a.ID || len(deps.Blocks) != 1 || deps.Blocks[0].ID != c.ID {
		t.Fatalf("B waits for A and C waits for B, got %+v", deps)
	}
}

func TestListSubIssueBlockersReturnsTheParentsGraph(t *testing.T) {
	dependencyTestSetup(t)

	parent := createDependencyTestIssue(t, map[string]any{"title": "Feature"})
	design := createDependencyTestIssue(t, map[string]any{"title": "Design", "parent_issue_id": parent.ID})
	build := createDependencyTestIssue(t, map[string]any{
		"title":           "Build",
		"parent_issue_id": parent.ID,
		"blocked_by":      []string{design.ID},
	})
	setDependencyTestIssueStatus(t, design.ID, "done")

	req := newRequest("GET", "/api/issues/"+parent.ID+"/sub-issue-blockers", nil)
	req = withURLParam(req, "id", parent.ID)
	w := testutil.Call(t, testHandler.ListSubIssueBlockers, req).Want(http.StatusOK)
	var graph SubIssueBlockersResponse
	json.NewDecoder(w.Body).Decode(&graph)
	if len(graph.Edges) != 1 || graph.Edges[0].IssueID != build.ID || graph.Edges[0].BlockedBy.ID != design.ID || !graph.Edges[0].BlockedBy.Done {
		t.Fatalf("the graph must hold Build waiting for the done Design, got %+v", graph.Edges)
	}
}
