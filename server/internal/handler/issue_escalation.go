package handler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Escalation of a stuck sub-issue to whoever runs its parent.
//
// A parent's assignee is woken when its sub-issues finish. Nothing woke it
// when one of them stopped without finishing: a sub-issue set to blocked, or
// one cancelled while others still wait for it, left the rest of the graph
// waiting with nobody told. The parent's assignee is the one who planned the
// work and can re-plan it, so it is told first; a person is only needed when
// it cannot decide either.

// RegisterIssueEscalationListeners wakes a parent's assignee when a sub-issue
// gets stuck.
func (h *Handler) RegisterIssueEscalationListeners() {
	h.Bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		if changed, _ := payload["status_changed"].(bool); !changed {
			return
		}
		var id string
		switch issue := payload["issue"].(type) {
		case IssueResponse:
			id = issue.ID
		case map[string]any:
			id, _ = issue["id"].(string)
		}
		issueID, err := util.ParseUUID(id)
		if err != nil {
			return
		}
		h.escalateStuckSubIssue(context.Background(), issueID, e.ActorType, e.ActorID)
	})
}

// escalateStuckSubIssue starts a run for the parent's assignee when the
// sub-issue just became stuck. It reads the stored row, so a status that has
// moved on again decides.
func (h *Handler) escalateStuckSubIssue(ctx context.Context, issueID pgtype.UUID, actorType, actorID string) {
	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err != nil || !issue.ParentIssueID.Valid {
		return
	}
	note := h.stuckSubIssueNote(ctx, issue)
	if note == "" {
		return
	}
	parent, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issue.ParentIssueID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return
	}
	// A finished parent has nobody left to re-plan it.
	if category := issuestatus.Category(ctx, h.Queries, parent.WorkspaceID, parent.Status); category != issuestatus.CategoryUnstarted && category != issuestatus.CategoryStarted {
		return
	}
	trigger, ok := h.IssueService.WillEnqueueRun(ctx, service.IssueTriggerInput{
		Issue:           parent,
		PrevStatus:      parent.Status,
		AssigneeChanged: true,
	}, service.IssueTriggerProbe{})
	if !ok {
		return
	}
	// The parent's own assignee stopping a sub-issue already knows.
	if actorType == "agent" && actorID == uuidToString(trigger.AgentID) {
		return
	}
	slog.Info("stuck sub-issue escalated to its parent",
		"issue_id", uuidToString(issue.ID), "parent_id", uuidToString(parent.ID), "agent_id", uuidToString(trigger.AgentID))
	h.dispatchIssueRun(ctx, parent, trigger, "system", "", note)
}

// stuckSubIssueNote says why the sub-issue needs its parent's attention, or
// returns "" when it does not: it is neither blocked nor a cancelled issue
// that others still wait for.
func (h *Handler) stuckSubIssueNote(ctx context.Context, issue db.Issue) string {
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	name := prefix + "-" + strconv.Itoa(int(issue.Number))
	switch issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) {
	case "blocked":
		return "Sub-issue " + name + " is blocked, and anything that waits for it will not start. " +
			"Read its latest comments, then resolve it or re-plan around it. " +
			"Ask a person only for what you cannot decide."
	case "cancelled":
		dependents, err := h.Queries.ListIssueDependents(ctx, db.ListIssueDependentsParams{BlockerID: issue.ID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return ""
		}
		var waiting []string
		for _, d := range dependents {
			if issuestatus.Category(ctx, h.Queries, d.WorkspaceID, d.Status) == issuestatus.CategoryUnstarted {
				waiting = append(waiting, prefix+"-"+strconv.Itoa(int(d.Number)))
			}
		}
		if len(waiting) == 0 {
			return ""
		}
		return "Sub-issue " + name + " was cancelled, and " + strings.Join(waiting, ", ") +
			" still wait for it and will never start. Remove those dependencies, replace the cancelled work, or cancel what depended on it."
	}
	return ""
}
