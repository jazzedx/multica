package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// startStepRun moves an issue into a step as a member and returns the run the
// step's handler gets, marked running.
func startStepRun(t *testing.T, issueID, agentID, status string) string {
	t.Helper()
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE issue_id = $1 AND status IN ('queued', 'dispatched', 'running')`, issueID)
	updateIssueForTest(t, issueID, map[string]any{"status": status}).Want(http.StatusOK)
	var taskID string
	dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued' ORDER BY created_at DESC LIMIT 1`, issueID, agentID).Scan(&taskID)
	if taskID == "" {
		t.Fatalf("entering %s queued no run for the step's handler", status)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, taskID)
	return taskID
}

// recordStatusMove writes the activity row the server's listener records for
// a status change; handler tests run without the listeners.
func recordStatusMove(t *testing.T, issueID, agentID, from, to string) {
	t.Helper()
	dbfx.Exec(t, `INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details)
		VALUES ($1, $2, 'agent', $3, 'status_changed', jsonb_build_object('from', $4::text, 'to', $5::text))`,
		testWorkspaceID, issueID, agentID, from, to)
}

// A strict step turns its next and back statuses from advice into the only
// moves its run can make: marking the work done without the review is refused.
func TestStrictStepRefusesARunSkippingAhead(t *testing.T) {
	seedTestCatalog(t)
	agentID := seededReadyAgentID(t)
	projectID := createWorkflowTestProject(t, "Workflow strict step project")
	setColumnHandoff(t, projectID, "in_progress", map[string]any{
		"handler": map[string]any{"type": "agent", "id": agentID}, "instructions": "Implement it.",
		"next_status_key": "in_review", "strict": true,
	}).Want(http.StatusOK)
	issueID := dbfx.Issue(t, "strict step", testutil.Cols{"project_id": projectID, "status": "todo"})
	taskID := startStepRun(t, issueID, agentID, "in_progress")

	body := agentUpdateIssue(t, issueID, agentID, taskID, map[string]any{"status": "done"}).Want(http.StatusConflict).Map()
	if body["code"] != "workflow_move_not_allowed" {
		t.Fatalf("refusal = %v", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "in_review") || !strings.Contains(msg, "blocked") {
		t.Fatalf("refusal %q does not say what the run may do", msg)
	}
	if status, _, _ := issueStatusAndAssignee(t, issueID); status != "in_progress" {
		t.Fatalf("a refused move changed the status to %q", status)
	}
	if brief := claimBriefForTest(t, issueID, db.AgentTaskQueue{AgentID: util.MustParseUUID(agentID)}); !strings.Contains(brief, "only status moves this step accepts") {
		t.Fatalf("the run is not told the step is strict:\n%s", brief)
	}

	agentUpdateIssue(t, issueID, agentID, taskID, map[string]any{"status": "in_review"}).Want(http.StatusOK)
}

// The rule binds the run, not people: someone has to be able to overrule it.
func TestStrictStepDoesNotBindMembers(t *testing.T) {
	seedTestCatalog(t)
	agentID := seededReadyAgentID(t)
	projectID := createWorkflowTestProject(t, "Workflow strict member project")
	setColumnHandoff(t, projectID, "in_progress", map[string]any{
		"handler": map[string]any{"type": "agent", "id": agentID}, "instructions": "Implement it.",
		"next_status_key": "in_review", "strict": true,
	}).Want(http.StatusOK)
	issueID := dbfx.Issue(t, "strict step, member", testutil.Cols{"project_id": projectID, "status": "todo"})
	startStepRun(t, issueID, agentID, "in_progress")

	updateIssueForTest(t, issueID, map[string]any{"status": "done"}).Want(http.StatusOK)
}

// A reviewing step may send an issue back only so many times. Past that the
// run has to stop with blocked, which is what reaches whoever runs the parent.
func TestReturnLimitRefusesOneReturnTooMany(t *testing.T) {
	seedTestCatalog(t)
	agentID := seededReadyAgentID(t)
	projectID := createWorkflowTestProject(t, "Workflow return limit project")
	setColumnHandoff(t, projectID, "in_review", map[string]any{
		"handler": map[string]any{"type": "agent", "id": agentID}, "instructions": "Review it.",
		"back_status_key": "in_progress", "max_returns": 2,
	}).Want(http.StatusOK)
	issueID := dbfx.Issue(t, "return limit", testutil.Cols{"project_id": projectID, "status": "in_progress"})

	for round := 1; round <= 2; round++ {
		taskID := startStepRun(t, issueID, agentID, "in_review")
		agentUpdateIssue(t, issueID, agentID, taskID, map[string]any{"status": "in_progress"}).Want(http.StatusOK)
		recordStatusMove(t, issueID, agentID, "in_review", "in_progress")
	}

	taskID := startStepRun(t, issueID, agentID, "in_review")
	body := agentUpdateIssue(t, issueID, agentID, taskID, map[string]any{"status": "in_progress"}).Want(http.StatusConflict).Map()
	if body["code"] != "workflow_return_limit" || body["limit"] != float64(2) {
		t.Fatalf("refusal = %v", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "blocked") {
		t.Fatalf("refusal %q does not send the run to blocked", msg)
	}
	// Blocked is always open to the run, and a member can still send it back.
	agentUpdateIssue(t, issueID, agentID, taskID, map[string]any{"status": "blocked"}).Want(http.StatusOK)
	updateIssueForTest(t, issueID, map[string]any{"status": "in_progress"}).Want(http.StatusOK)
}

func TestReturnLimitNeedsBlockedInTheWorkflow(t *testing.T) {
	seedTestCatalog(t)
	agentID := seededReadyAgentID(t)
	workflow := createTestWorkflowSteps(t, "No blocked "+workflowTestSuffix(), statusSteps("in_progress", "in_review"))

	setWorkflowHandoff(t, workflow.ID, "in_review", map[string]any{
		"handler": map[string]any{"type": "agent", "id": agentID}, "instructions": "Review it.",
		"back_status_key": "in_progress", "max_returns": 2,
	}).Want(http.StatusBadRequest)
}
