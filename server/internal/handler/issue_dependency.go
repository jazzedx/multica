package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/projectaccess"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// IssueDependencyRef is the other end of a dependency.
type IssueDependencyRef struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	// Done is whether the issue is completed, which is what a waiting issue
	// needs from it. A cancelled issue is not done.
	Done bool `json:"done"`
}

// IssueDependenciesResponse lists what an issue waits for and what waits for it.
type IssueDependenciesResponse struct {
	BlockedBy []IssueDependencyRef `json:"blocked_by"`
	Blocks    []IssueDependencyRef `json:"blocks"`
}

// SubIssueBlocker is one edge of a parent's graph: a sub-issue and an issue it
// waits for.
type SubIssueBlocker struct {
	IssueID   string             `json:"issue_id"`
	BlockedBy IssueDependencyRef `json:"blocked_by"`
}

type SubIssueBlockersResponse struct {
	Edges []SubIssueBlocker `json:"edges"`
}

type addIssueDependencyRequest struct {
	// BlockedBy is the id or identifier of the issue to wait for.
	BlockedBy string `json:"blocked_by"`
}

func (h *Handler) issueDependencyRef(ctx context.Context, prefix string, id, workspaceID pgtype.UUID, number int32, title, status string) IssueDependencyRef {
	return IssueDependencyRef{
		ID:         uuidToString(id),
		Identifier: prefix + "-" + strconv.Itoa(int(number)),
		Title:      title,
		Status:     status,
		Done:       issuestatus.Effective(ctx, h.Queries, workspaceID, status) == "done",
	}
}

// issueDependencies builds both directions for one issue, leaving out issues
// in projects the caller cannot see.
func (h *Handler) issueDependencies(ctx context.Context, issue db.Issue, hidden projectaccess.Hidden) (IssueDependenciesResponse, error) {
	resp := IssueDependenciesResponse{BlockedBy: []IssueDependencyRef{}, Blocks: []IssueDependencyRef{}}
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)

	blockers, err := h.Queries.ListIssueBlockers(ctx, db.ListIssueBlockersParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return resp, err
	}
	for _, b := range blockers {
		if hidden.CanSeeProject(b.ProjectID) {
			resp.BlockedBy = append(resp.BlockedBy, h.issueDependencyRef(ctx, prefix, b.ID, b.WorkspaceID, b.Number, b.Title, b.Status))
		}
	}
	dependents, err := h.Queries.ListIssueDependents(ctx, db.ListIssueDependentsParams{BlockerID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return resp, err
	}
	for _, d := range dependents {
		if hidden.CanSeeProject(d.ProjectID) {
			resp.Blocks = append(resp.Blocks, h.issueDependencyRef(ctx, prefix, d.ID, d.WorkspaceID, d.Number, d.Title, d.Status))
		}
	}
	return resp, nil
}

func (h *Handler) writeIssueDependencies(w http.ResponseWriter, r *http.Request, issue db.Issue) {
	hidden, ok := h.requestHiddenProjects(w, r, uuidToString(issue.WorkspaceID))
	if !ok {
		return
	}
	resp, err := h.issueDependencies(r.Context(), issue, hidden)
	if err != nil {
		slog.Warn("list issue dependencies failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list issue dependencies")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListIssueDependencies — GET /api/issues/{id}/dependencies
func (h *Handler) ListIssueDependencies(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	h.writeIssueDependencies(w, r, issue)
}

// ListSubIssueBlockers — GET /api/issues/{id}/sub-issue-blockers
//
// One read for a parent's whole graph, so a list of sub-issues can say which
// of them are waiting without a request per row.
func (h *Handler) ListSubIssueBlockers(w http.ResponseWriter, r *http.Request) {
	parent, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	hidden, ok := h.requestHiddenProjects(w, r, uuidToString(parent.WorkspaceID))
	if !ok {
		return
	}
	rows, err := h.Queries.ListSubIssueBlockers(r.Context(), db.ListSubIssueBlockersParams{
		ParentIssueID: parent.ID,
		WorkspaceID:   parent.WorkspaceID,
	})
	if err != nil {
		slog.Warn("list sub-issue blockers failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(parent.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list sub-issue blockers")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), parent.WorkspaceID)
	resp := SubIssueBlockersResponse{Edges: []SubIssueBlocker{}}
	for _, row := range rows {
		if !hidden.CanSeeProject(row.ProjectID) {
			continue
		}
		resp.Edges = append(resp.Edges, SubIssueBlocker{
			IssueID:   uuidToString(row.IssueID),
			BlockedBy: h.issueDependencyRef(r.Context(), prefix, row.ID, row.WorkspaceID, row.Number, row.Title, row.Status),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// AddIssueDependency — POST /api/issues/{id}/dependencies
//
// Makes the issue wait for another one. It does not stop a run the issue
// already has; it decides when the next one may start.
func (h *Handler) AddIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req addIssueDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.BlockedBy) == "" {
		writeError(w, http.StatusBadRequest, "blocked_by is required")
		return
	}
	blocker, ok := h.loadIssueForUser(w, r, strings.TrimSpace(req.BlockedBy))
	if !ok {
		return
	}
	if blocker.ID == issue.ID {
		writeError(w, http.StatusBadRequest, "an issue cannot wait for itself")
		return
	}
	cycle, err := h.Queries.IssueDependsOn(r.Context(), db.IssueDependsOnParams{IssueID: blocker.ID, TargetID: issue.ID})
	if err != nil {
		slog.Warn("issue dependency cycle check failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add the dependency")
		return
	}
	if cycle {
		writeError(w, http.StatusConflict, "that issue already waits for this one")
		return
	}
	if _, err := h.Queries.AddIssueBlocker(r.Context(), db.AddIssueBlockerParams{IssueID: issue.ID, BlockerID: blocker.ID}); err != nil {
		slog.Warn("add issue dependency failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add the dependency")
		return
	}
	h.writeIssueDependencies(w, r, issue)
}

// RemoveIssueDependency — DELETE /api/issues/{id}/dependencies/{blockerId}
//
// Removing the last thing an issue waited for starts it, exactly as that
// thing being done would have.
func (h *Handler) RemoveIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	blocker, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "blockerId"))
	if !ok {
		return
	}
	removed, err := h.Queries.RemoveIssueBlocker(r.Context(), db.RemoveIssueBlockerParams{IssueID: issue.ID, BlockerID: blocker.ID})
	if err != nil {
		slog.Warn("remove issue dependency failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove the dependency")
		return
	}
	if removed > 0 {
		h.startIssueIfUnblocked(r.Context(), issue)
	}
	h.writeIssueDependencies(w, r, issue)
}

// resolveCreateBlockers turns the ids or identifiers a create request names
// into issues of this workspace the caller can see. A new issue cannot close a
// cycle, so there is nothing else to check.
func (h *Handler) resolveCreateBlockers(w http.ResponseWriter, r *http.Request, refs []string) ([]pgtype.UUID, bool) {
	ids := make([]pgtype.UUID, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		blocker, ok := h.loadIssueForUser(w, r, ref)
		if !ok {
			return nil, false
		}
		if key := uuidToString(blocker.ID); !seen[key] {
			seen[key] = true
			ids = append(ids, blocker.ID)
		}
	}
	return ids, true
}

// RegisterIssueDependencyListeners starts waiting issues when what they wait
// for is done. It listens on the bus rather than sitting in the update
// handler because an issue is completed from several places, a merged pull
// request among them.
func (h *Handler) RegisterIssueDependencyListeners() {
	h.Bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		if changed, _ := payload["status_changed"].(bool); !changed {
			return
		}
		// HTTP writers send an IssueResponse, background writers a map.
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
		h.releaseIssueDependents(context.Background(), issueID)
	})
}

// releaseIssueDependents starts every issue that was waiting only for this
// one. It reads the stored row, so a status that moved on again decides.
func (h *Handler) releaseIssueDependents(ctx context.Context, blockerID pgtype.UUID) {
	blocker, err := h.Queries.GetIssue(ctx, blockerID)
	if err != nil || issuestatus.Effective(ctx, h.Queries, blocker.WorkspaceID, blocker.Status) != "done" {
		return
	}
	dependents, err := h.Queries.ListIssueDependents(ctx, db.ListIssueDependentsParams{BlockerID: blocker.ID, WorkspaceID: blocker.WorkspaceID})
	if err != nil {
		slog.Warn("list issue dependents failed", "issue_id", uuidToString(blocker.ID), "error", err)
		return
	}
	for _, d := range dependents {
		issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: d.ID, WorkspaceID: d.WorkspaceID})
		if err != nil {
			continue
		}
		h.startIssueIfUnblocked(ctx, issue)
	}
}

// startIssueIfUnblocked starts the run a waiting issue was held from, once
// nothing holds it. Only work nobody has begun starts this way: an issue
// parked in backlog stays parked, and one already moving is left to whoever
// moved it.
func (h *Handler) startIssueIfUnblocked(ctx context.Context, issue db.Issue) {
	if issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) == "backlog" ||
		issuestatus.Category(ctx, h.Queries, issue.WorkspaceID, issue.Status) != issuestatus.CategoryUnstarted {
		return
	}
	// The same decision an assignment makes, which also declines while
	// anything still blocks the issue.
	trigger, ok := h.IssueService.WillEnqueueRun(ctx, service.IssueTriggerInput{
		Issue:           issue,
		PrevStatus:      issue.Status,
		AssigneeChanged: true,
	}, service.IssueTriggerProbe{})
	if !ok {
		return
	}
	slog.Info("issue released by its dependencies", "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(trigger.AgentID))
	h.dispatchIssueRun(ctx, issue, trigger, "system", "", h.releasedIssueNote(ctx, issue))
}

// releasedIssueNote tells the run which issues it was waiting for, since
// their outcome is its input.
func (h *Handler) releasedIssueNote(ctx context.Context, issue db.Issue) string {
	blockers, err := h.Queries.ListIssueBlockers(ctx, db.ListIssueBlockersParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil || len(blockers) == 0 {
		return ""
	}
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	names := make([]string, len(blockers))
	for i, b := range blockers {
		names[i] = prefix + "-" + strconv.Itoa(int(b.Number))
	}
	return "This issue was waiting for " + strings.Join(names, ", ") +
		", now done. Read what they delivered before you start."
}
