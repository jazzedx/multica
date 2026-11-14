package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/issueworkflow"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// IssueDeliveryOverviewResponse says what it took to deliver an issue and
// everything under it: how far the work is, how often it ran, how often it
// went back, and how often a person had to step in. Token use and run time
// are not repeated here; the execution log already sums them for a sub-tree.
type IssueDeliveryOverviewResponse struct {
	SubIssues IssueDeliverySubIssues `json:"sub_issues"`
	Runs      IssueDeliveryRuns      `json:"runs"`
	// ReworkRounds counts the times an issue went back to an earlier place on
	// its board, whoever sent it, including by way of blocked.
	ReworkRounds int `json:"rework_rounds"`
	// HumanActions counts what members did on these issues after the root was
	// filed: recorded changes and comments.
	HumanActions       int    `json:"human_actions"`
	PullRequestsMerged int    `json:"pull_requests_merged"`
	StartedAt          string `json:"started_at"`
	// FinishedAt is when the root last became done, while it still is.
	FinishedAt *string `json:"finished_at"`
}

type IssueDeliverySubIssues struct {
	Total     int `json:"total"`
	Done      int `json:"done"`
	Cancelled int `json:"cancelled"`
}

type IssueDeliveryRuns struct {
	Total  int `json:"total"`
	Failed int `json:"failed"`
	Active int `json:"active"`
}

// Statuses that are not places on the way to done: a move into or out of one
// is parking, stopping or giving up, not going back.
var deliverySideStatuses = map[string]bool{
	issuestatus.Backlog:   true,
	issuestatus.Blocked:   true,
	issuestatus.Cancelled: true,
}

// isReworkMove reports whether a status change sent the issue back to an
// earlier place on the board that orders its statuses.
func isReworkMove(order []string, from, to string) bool {
	if from == to || deliverySideStatuses[from] || deliverySideStatuses[to] {
		return false
	}
	fromAt, toAt := -1, -1
	for i, key := range order {
		if key == from {
			fromAt = i
		}
		if key == to {
			toAt = i
		}
	}
	return fromAt >= 0 && toAt >= 0 && toAt < fromAt
}

// GetIssueDeliveryOverview — GET /api/issues/{id}/delivery-overview
func (h *Handler) GetIssueDeliveryOverview(w http.ResponseWriter, r *http.Request) {
	root, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	hidden, ok := h.requestHiddenProjects(w, r, uuidToString(root.WorkspaceID))
	if !ok {
		return
	}
	ctx := r.Context()
	fail := func(step string, err error) {
		slog.Warn("delivery overview failed", append(logger.RequestAttrs(r), "step", step, "error", err, "issue_id", uuidToString(root.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to build the delivery overview")
	}

	tree, err := h.Queries.ListIssueSubtree(ctx, db.ListIssueSubtreeParams{IssueID: root.ID, WorkspaceID: root.WorkspaceID})
	if err != nil {
		fail("subtree", err)
		return
	}
	resp := IssueDeliveryOverviewResponse{StartedAt: root.CreatedAt.Time.UTC().Format(time.RFC3339)}
	ids := make([]pgtype.UUID, 0, len(tree))
	projectOf := map[pgtype.UUID]pgtype.UUID{}
	for _, node := range tree {
		// Work in a project the caller cannot see is not theirs to count.
		if !hidden.CanSeeProject(node.ProjectID) {
			continue
		}
		ids = append(ids, node.ID)
		projectOf[node.ID] = node.ProjectID
		if node.Depth == 0 {
			continue
		}
		resp.SubIssues.Total++
		switch issuestatus.Effective(ctx, h.Queries, root.WorkspaceID, node.Status) {
		case "done":
			resp.SubIssues.Done++
		case "cancelled":
			resp.SubIssues.Cancelled++
		}
	}

	runs, err := h.Queries.CountRunsByIssues(ctx, ids)
	if err != nil {
		fail("runs", err)
		return
	}
	resp.Runs = IssueDeliveryRuns{Total: int(runs.Total), Failed: int(runs.Failed), Active: int(runs.Active)}

	moves, err := h.Queries.ListStatusMovesByIssues(ctx, ids)
	if err != nil {
		fail("status moves", err)
		return
	}
	orders := map[pgtype.UUID][]string{}
	// Where each issue last stood on the way to done. A trip through blocked
	// (or backlog, or cancelled) is judged by where the issue was before it
	// and where it came out: review, blocked, then back to todo is rework all
	// the same.
	lastPlace := map[pgtype.UUID]string{}
	rootDone := issuestatus.Effective(ctx, h.Queries, root.WorkspaceID, root.Status) == "done"
	for _, move := range moves {
		projectID := projectOf[move.IssueID]
		order, known := orders[projectID]
		if !known {
			order = h.deliveryStatusOrder(ctx, root.WorkspaceID, projectID)
			orders[projectID] = order
		}
		from := move.FromStatus
		if deliverySideStatuses[from] {
			if place, ok := lastPlace[move.IssueID]; ok {
				from = place
			}
		} else {
			lastPlace[move.IssueID] = from
		}
		if isReworkMove(order, from, move.ToStatus) {
			resp.ReworkRounds++
		}
		if !deliverySideStatuses[move.ToStatus] {
			lastPlace[move.IssueID] = move.ToStatus
		}
		if rootDone && move.IssueID == root.ID && issuestatus.Effective(ctx, h.Queries, root.WorkspaceID, move.ToStatus) == "done" {
			finished := move.CreatedAt.Time.UTC().Format(time.RFC3339)
			resp.FinishedAt = &finished
		}
	}

	human, err := h.Queries.CountMemberActionsByIssues(ctx, db.CountMemberActionsByIssuesParams{IssueIds: ids, RootID: root.ID})
	if err != nil {
		fail("member actions", err)
		return
	}
	resp.HumanActions = int(human)

	merged, err := h.Queries.CountMergedPullRequestsByIssues(ctx, ids)
	if err != nil {
		fail("pull requests", err)
		return
	}
	resp.PullRequestsMerged = int(merged)

	writeJSON(w, http.StatusOK, resp)
}

// deliveryStatusOrder is the board order of the statuses an issue of this
// project moves through: its workflow's, or the built-in order when the
// workspace has stored none.
func (h *Handler) deliveryStatusOrder(ctx context.Context, workspaceID, projectID pgtype.UUID) []string {
	var def *issueworkflow.Definition
	if projectID.Valid {
		def, _, _ = issueworkflow.ForProject(ctx, h.Queries, workspaceID, projectID)
	} else {
		def, _ = issueworkflow.OutsideProject(ctx, h.Queries, workspaceID)
	}
	if def == nil {
		return issueworkflow.BuiltInDefaultKeys()
	}
	return def.StatusKeys()
}
