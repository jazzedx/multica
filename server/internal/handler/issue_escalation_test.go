package handler

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

var escalationListenersOnce sync.Once

func escalationTestSetup(t *testing.T) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	escalationListenersOnce.Do(testHandler.RegisterIssueEscalationListeners)
}

// parentRunNotes returns the handoff notes of the runs queued on an issue.
func queuedRunNotes(t *testing.T, issueID, agentID string) []string {
	t.Helper()
	rows, err := testPool.Query(t.Context(),
		`SELECT COALESCE(handoff_note, '') FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued' ORDER BY created_at`,
		issueID, agentID)
	if err != nil {
		t.Fatalf("list queued runs: %v", err)
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatalf("scan note: %v", err)
		}
		notes = append(notes, note)
	}
	return notes
}

func TestBlockedSubIssueWakesTheParentsAssignee(t *testing.T) {
	escalationTestSetup(t)
	agentID := createHandlerTestAgent(t, "Escalation Planner Agent", nil)

	// Backlog first, so creating the parent queues nothing of its own.
	parent := createDependencyTestIssue(t, map[string]any{
		"title": "Feature", "status": "backlog", "assignee_type": "agent", "assignee_id": agentID,
	})
	testPool.Exec(t.Context(), `UPDATE issue SET status = 'in_progress' WHERE id = $1`, parent.ID)
	child := createDependencyTestIssue(t, map[string]any{"title": "Hard part", "parent_issue_id": parent.ID})

	setDependencyTestIssueStatus(t, child.ID, "in_progress")
	if notes := queuedRunNotes(t, parent.ID, agentID); len(notes) != 0 {
		t.Fatalf("a sub-issue that is only moving must not wake the parent, got %v", notes)
	}

	setDependencyTestIssueStatus(t, child.ID, "blocked")
	notes := queuedRunNotes(t, parent.ID, agentID)
	if len(notes) != 1 || !strings.Contains(notes[0], child.Identifier) || !strings.Contains(notes[0], "is blocked") {
		t.Fatalf("a blocked sub-issue must wake the parent's assignee once, naming it; got %v", notes)
	}
}

func TestCancelledSubIssueWakesTheParentOnlyWhenOthersWaitForIt(t *testing.T) {
	escalationTestSetup(t)
	agentID := createHandlerTestAgent(t, "Escalation Cancel Agent", nil)

	parent := createDependencyTestIssue(t, map[string]any{
		"title": "Feature", "status": "backlog", "assignee_type": "agent", "assignee_id": agentID,
	})
	testPool.Exec(t.Context(), `UPDATE issue SET status = 'in_progress' WHERE id = $1`, parent.ID)
	alone := createDependencyTestIssue(t, map[string]any{"title": "Nobody waits for this", "parent_issue_id": parent.ID})
	upstream := createDependencyTestIssue(t, map[string]any{"title": "Upstream", "parent_issue_id": parent.ID})
	downstream := createDependencyTestIssue(t, map[string]any{
		"title": "Downstream", "parent_issue_id": parent.ID, "blocked_by": []string{upstream.ID},
	})

	setDependencyTestIssueStatus(t, alone.ID, "cancelled")
	if notes := queuedRunNotes(t, parent.ID, agentID); len(notes) != 0 {
		t.Fatalf("cancelling work nothing waits for strands nothing, got %v", notes)
	}

	setDependencyTestIssueStatus(t, upstream.ID, "cancelled")
	notes := queuedRunNotes(t, parent.ID, agentID)
	if len(notes) != 1 || !strings.Contains(notes[0], upstream.Identifier) || !strings.Contains(notes[0], downstream.Identifier) {
		t.Fatalf("a cancelled sub-issue others wait for must wake the parent, naming both; got %v", notes)
	}
}

func TestStuckSubIssueOfAFinishedOrUnassignedParentWakesNobody(t *testing.T) {
	escalationTestSetup(t)
	agentID := createHandlerTestAgent(t, "Escalation Done Agent", nil)

	finished := createDependencyTestIssue(t, map[string]any{
		"title": "Shipped", "status": "backlog", "assignee_type": "agent", "assignee_id": agentID,
	})
	testPool.Exec(t.Context(), `UPDATE issue SET status = 'done' WHERE id = $1`, finished.ID)
	late := createDependencyTestIssue(t, map[string]any{"title": "Late follow-up", "parent_issue_id": finished.ID})
	setDependencyTestIssueStatus(t, late.ID, "blocked")
	if notes := queuedRunNotes(t, finished.ID, agentID); len(notes) != 0 {
		t.Fatalf("a finished parent has nobody to re-plan it, got %v", notes)
	}

	unowned := createDependencyTestIssue(t, map[string]any{"title": "No assignee"})
	orphan := createDependencyTestIssue(t, map[string]any{"title": "Stuck", "parent_issue_id": unowned.ID})
	req := newRequest("PUT", "/api/issues/"+orphan.ID, map[string]any{"status": "blocked"})
	req = withURLParam(req, "id", orphan.ID)
	testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusOK)
	var runs int
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, unowned.ID).Scan(&runs)
	if runs != 0 {
		t.Fatalf("a parent without an agent has no run to start, got %d", runs)
	}
}
