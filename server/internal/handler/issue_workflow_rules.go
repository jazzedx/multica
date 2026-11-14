package handler

import (
	"context"
	"strconv"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/issueworkflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// checkWorkflowStepRules applies the current step's rules to a status change
// made by the run that works the issue. Members, and agents acting on an
// issue they are not running, are not bound: run is nil for them.
func (h *Handler) checkWorkflowStepRules(ctx context.Context, def *issueworkflow.Definition, prev db.Issue, params *db.UpdateIssueParams, statusExplicit bool, run *db.AgentTaskQueue) error {
	if run == nil || def == nil || !statusExplicit || !params.Status.Valid || params.Status.String == prev.Status {
		return nil
	}
	from, to := prev.Status, params.Status.String
	identifier := h.getIssuePrefix(ctx, prev.WorkspaceID) + "-" + strconv.Itoa(int(prev.Number))
	resolver := issuestatus.NewResolver(prev.WorkspaceID)
	effective := func(key string) string {
		return resolver.Effective(ctx, h.Queries, key)
	}
	if err := issueworkflow.CheckMove(def, identifier, from, to, effective); err != nil {
		return err
	}
	limit, counted := issueworkflow.ReturnLimit(def, from, to)
	if !counted {
		return nil
	}
	returns, err := h.Queries.CountIssueStatusMoves(ctx, db.CountIssueStatusMovesParams{
		IssueID:    prev.ID,
		FromStatus: from,
		ToStatus:   to,
	})
	if err != nil {
		return err
	}
	if returns >= int64(limit) {
		return &issueworkflow.ReturnLimitError{IssueIdentifier: identifier, From: from, To: to, Limit: limit}
	}
	return nil
}
