package service

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Issue dependencies turn a parent's sub-issues into a graph: an issue that
// waits for another one starts no run from a write to itself, and starts when
// the last issue it waits for is done. Nothing else about the issue changes
// while it waits; its status stays a statement about the work, not about the
// scheduler.

// OpenIssueBlockers returns the issues `issue` still waits for: those it is
// blocked by that have not been completed. A cancelled blocker stays open,
// because what the waiting issue needed from it was never delivered; a person
// settles that by removing the dependency.
func OpenIssueBlockers(ctx context.Context, q *db.Queries, issue db.Issue) ([]db.ListIssueBlockersRow, error) {
	blockers, err := q.ListIssueBlockers(ctx, db.ListIssueBlockersParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	open := make([]db.ListIssueBlockersRow, 0, len(blockers))
	for _, blocker := range blockers {
		if issuestatus.Effective(ctx, q, blocker.WorkspaceID, blocker.Status) != "done" {
			open = append(open, blocker)
		}
	}
	return open, nil
}

// waitsForBlockers reports whether a run for the issue must hold. A failed
// lookup does not hold: nearly every issue waits for nothing, and holding on
// an error would silently drop the start of all of them, with no later event
// to bring it back. An issue that does wait and starts early this way shows
// up as having gone ahead of what it depends on.
func (s *IssueService) waitsForBlockers(ctx context.Context, issue db.Issue) bool {
	open, err := OpenIssueBlockers(ctx, s.Queries, issue)
	if err != nil {
		slog.Warn("issue dependency lookup failed; not holding the run",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return false
	}
	return len(open) > 0
}

// addIssueBlockers records what a new issue waits for, inside the transaction
// that creates it, so the issue is never visible without them.
func addIssueBlockers(ctx context.Context, q *db.Queries, issueID pgtype.UUID, blockerIDs []pgtype.UUID) error {
	for _, blockerID := range blockerIDs {
		if _, err := q.AddIssueBlocker(ctx, db.AddIssueBlockerParams{IssueID: issueID, BlockerID: blockerID}); err != nil {
			return err
		}
	}
	return nil
}
