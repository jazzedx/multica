package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/issueworkflow"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestReworkIsAMoveBackAlongTheBoard(t *testing.T) {
	order := issueworkflow.BuiltInDefaultKeys()
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{"in_review", "in_progress", true},
		{"in_review", "todo", true},
		{"done", "in_progress", true},
		{"todo", "in_progress", false},
		{"in_review", "done", false},
		// Parking, stopping and giving up are not going back, and neither is
		// coming out of them.
		{"in_progress", "blocked", false},
		{"blocked", "todo", false},
		{"in_review", "cancelled", false},
		{"in_progress", "backlog", false},
		// A status the board does not list says nothing about direction.
		{"somewhere", "todo", false},
		{"in_review", "in_review", false},
	} {
		if got := isReworkMove(order, tc.from, tc.to); got != tc.want {
			t.Errorf("isReworkMove(%s -> %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func recordIssueActivity(t *testing.T, issueID, actorType, actorID, action, details string) {
	t.Helper()
	dbfx.Exec(t, `INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`, testWorkspaceID, issueID, actorType, actorID, action, details)
}

func TestIssueDeliveryOverviewCountsTheWholeSubtree(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Delivery Overview Agent", nil)

	root := createDependencyTestIssue(t, map[string]any{"title": "Feature"})
	built := createDependencyTestIssue(t, map[string]any{
		"title": "Build it", "parent_issue_id": root.ID, "assignee_type": "agent", "assignee_id": agentID,
	})
	nested := createDependencyTestIssue(t, map[string]any{"title": "A part of it", "parent_issue_id": built.ID})
	dropped := createDependencyTestIssue(t, map[string]any{"title": "Not needed", "parent_issue_id": root.ID})
	open := createDependencyTestIssue(t, map[string]any{"title": "Still to do", "parent_issue_id": root.ID})
	unrelated := createDependencyTestIssue(t, map[string]any{"title": "Someone else's"})
	t.Cleanup(func() {
		for _, id := range []string{root.ID, built.ID, nested.ID, dropped.ID, open.ID, unrelated.ID} {
			testPool.Exec(t.Context(), `DELETE FROM activity_log WHERE issue_id = $1`, id)
			testPool.Exec(t.Context(), `DELETE FROM comment WHERE issue_id = $1`, id)
		}
	})

	setDependencyTestIssueStatus(t, built.ID, "done")
	setDependencyTestIssueStatus(t, nested.ID, "done")
	setDependencyTestIssueStatus(t, dropped.ID, "cancelled")
	setDependencyTestIssueStatus(t, root.ID, "done")
	// One run failed and was retried; creating the assigned sub-issue queued the first.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'failed', completed_at = now() WHERE issue_id = $1`, built.ID)
	dbfx.Exec(t, `INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		SELECT agent_id, runtime_id, issue_id, 'completed', priority FROM agent_task_queue WHERE issue_id = $1 LIMIT 1`, built.ID)

	// What the server's listeners would have recorded; handler tests run without them.
	for _, move := range [][2]string{{"todo", "in_progress"}, {"in_progress", "in_review"}, {"in_review", "in_progress"}, {"in_progress", "blocked"}, {"blocked", "in_progress"}, {"in_progress", "in_review"}, {"in_review", "done"}} {
		recordIssueActivity(t, built.ID, "agent", agentID, "status_changed", `{"from":"`+move[0]+`","to":"`+move[1]+`"}`)
	}
	recordIssueActivity(t, nested.ID, "agent", agentID, "status_changed", `{"from":"in_review","to":"todo"}`)
	// Sent back by way of blocked: review, blocked, then todo again.
	for _, move := range [][2]string{{"todo", "in_review"}, {"in_review", "blocked"}, {"blocked", "todo"}} {
		recordIssueActivity(t, open.ID, "agent", agentID, "status_changed", `{"from":"`+move[0]+`","to":"`+move[1]+`"}`)
	}
	recordIssueActivity(t, root.ID, "member", testUserID, "created", `{}`)
	recordIssueActivity(t, root.ID, "member", testUserID, "status_changed", `{"from":"in_progress","to":"done"}`)
	recordIssueActivity(t, dropped.ID, "member", testUserID, "status_changed", `{"from":"todo","to":"cancelled"}`)
	dbfx.Exec(t, `INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type) VALUES ($1, $2, 'member', $3, 'Go with A.', 'comment')`, root.ID, testWorkspaceID, testUserID)
	dbfx.Exec(t, `INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type) VALUES ($1, $2, 'agent', $3, 'Done.', 'comment')`, built.ID, testWorkspaceID, agentID)
	// Noise on an issue outside the subtree must not be counted.
	recordIssueActivity(t, unrelated.ID, "member", testUserID, "status_changed", `{"from":"in_review","to":"todo"}`)

	req := withURLParam(newRequest("GET", "/api/issues/"+root.ID+"/delivery-overview", nil), "id", root.ID)
	var got IssueDeliveryOverviewResponse
	testutil.Call(t, testHandler.GetIssueDeliveryOverview, req).Want(http.StatusOK).JSON(&got)

	if got.SubIssues != (IssueDeliverySubIssues{Total: 4, Done: 2, Cancelled: 1}) {
		t.Errorf("sub-issues = %+v, want 4 in all, 2 done, 1 cancelled", got.SubIssues)
	}
	if got.Runs != (IssueDeliveryRuns{Total: 2, Failed: 1, Active: 0}) {
		t.Errorf("runs = %+v, want 2 in all, 1 failed", got.Runs)
	}
	// in_review -> in_progress on one sub-issue, in_review -> todo below it,
	// and review -> blocked -> todo on a third. Coming out of blocked where the
	// issue went in is not rework.
	if got.ReworkRounds != 3 {
		t.Errorf("rework rounds = %d, want 3", got.ReworkRounds)
	}
	// Two status changes and a comment by a member; filing the root is not one.
	if got.HumanActions != 3 {
		t.Errorf("human actions = %d, want 3", got.HumanActions)
	}
	if got.StartedAt == "" || got.FinishedAt == nil {
		t.Errorf("a done root needs both ends of its lead time: %+v", got)
	}
}

func TestIssueDeliveryOverviewOfAnOpenIssueHasNoFinish(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	root := createDependencyTestIssue(t, map[string]any{"title": "Just filed"})

	req := withURLParam(newRequest("GET", "/api/issues/"+root.ID+"/delivery-overview", nil), "id", root.ID)
	var got IssueDeliveryOverviewResponse
	testutil.Call(t, testHandler.GetIssueDeliveryOverview, req).Want(http.StatusOK).JSON(&got)

	if got.FinishedAt != nil || got.SubIssues.Total != 0 || got.Runs.Total != 0 || got.ReworkRounds != 0 || got.HumanActions != 0 || got.PullRequestsMerged != 0 {
		t.Errorf("a fresh issue has nothing to report yet: %+v", got)
	}
}
