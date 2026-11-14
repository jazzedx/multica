package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// The sub-issue graph draws its edges from the children list, so each child
// carries the blockers its detail read would.
func TestListChildIssuesCarriesBlockers(t *testing.T) {
	parent := blockerTestIssue(t, map[string]any{"status": "todo"})
	first := blockerTestIssue(t, map[string]any{"status": "backlog", "parent_issue_id": parent.ID})
	second := blockerTestIssue(t, map[string]any{"status": "backlog", "parent_issue_id": parent.ID, "blocked_by": []string{first.ID}})

	var out struct {
		Issues []IssueResponse `json:"issues"`
	}
	req := withURLParam(newRequest("GET", "/api/issues/"+parent.ID+"/children", nil), "id", parent.ID)
	testutil.Call(t, testHandler.ListChildIssues, req).Want(http.StatusOK).JSON(&out)

	byID := map[string]IssueResponse{}
	for _, child := range out.Issues {
		byID[child.ID] = child
	}
	if got := byID[first.ID].BlockedBy; len(got) != 0 {
		t.Fatalf("first child blocked_by = %+v, want none", got)
	}
	got := byID[second.ID].BlockedBy
	if len(got) != 1 || got[0].ID != first.ID || got[0].Via != "manual" || got[0].Closed {
		t.Fatalf("second child blocked_by = %+v, want an open manual edge to %s", got, first.ID)
	}
	if byID[second.ID].BlockedCount != 1 {
		t.Fatalf("second child blocked_count = %d, want 1", byID[second.ID].BlockedCount)
	}
}
