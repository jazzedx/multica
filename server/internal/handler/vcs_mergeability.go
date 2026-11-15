package handler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Merge conflicts on Forgejo / Gitea / GitLab pull requests. A pull request
// starts conflicting when its base moves, which is an event of some other
// pull request or of none, so the verdict is read from the provider:
//
//   - after an event of the pull request itself;
//   - for the other open pull requests of a repository, after one merges;
//   - on a timer, for the pull requests a "wake on merge conflict" rule
//     watches, which also covers a push straight to the base branch and a
//     webhook that never arrived.
//
// The stored verdict is what the wakeup rule and the pull request card read.

var (
	// A provider checks a pull request shortly after its event, not within it.
	vcsMergeabilitySettleDelay = 3 * time.Second
	// How long "cannot be merged" has to hold, for one head and base, before
	// it is taken for a conflict rather than a check still running.
	vcsMergeabilityConfirmDelay = 15 * time.Second
	// Watched pull requests are read again once their verdict is this old.
	vcsMergeabilityMaxAge = 3 * time.Minute
)

const (
	vcsMergeabilityReadTimeout = 20 * time.Second
	vcsMergeabilitySweepBatch  = 50
)

// vcsReadMergeability asks the provider about one pull request. A variable so
// tests can stand in for the provider.
var vcsReadMergeability = func(ctx context.Context, conn db.VcsConnection, token string, pr db.VcsPullRequest) (vcs.Mergeability, error) {
	provider, ok := vcs.For(conn.Provider)
	if !ok {
		return vcs.Mergeability{}, errors.New("unknown provider " + conn.Provider)
	}
	reader, ok := provider.(vcs.MergeabilityReader)
	if !ok {
		return vcs.Mergeability{State: vcs.MergeStateUnknown}, nil
	}
	return reader.PullRequestMergeability(ctx, conn.InstanceUrl, token, pr.RepoOwner, pr.RepoName, pr.PrNumber)
}

// settleVCSMergeability turns readings into a verdict. "Cannot be merged" is
// read a second time after `wait`: it stands as a conflict only when it holds
// for the same head and base. If either moved in between, the provider is
// checking afresh and nothing is known yet.
func settleVCSMergeability(read func() (vcs.Mergeability, error), wait func()) (vcs.Mergeability, error) {
	first, err := read()
	if err != nil || first.State != vcs.MergeStateUnsettled {
		return first, err
	}
	wait()
	second, err := read()
	if err != nil || second.State != vcs.MergeStateUnsettled {
		return second, err
	}
	if second.HeadSHA == first.HeadSHA && second.BaseSHA == first.BaseSHA {
		second.State = vcs.MergeStateConflicting
	} else {
		second.State = vcs.MergeStateUnknown
	}
	return second, nil
}

// vcsMergeabilityInflight holds the pull requests being read, so events that
// arrive together cost one read.
var vcsMergeabilityInflight sync.Map

// afterVCSPullRequestMirrored schedules the reads an event calls for.
func (h *Handler) afterVCSPullRequestMirrored(conn db.VcsConnection, pr db.VcsPullRequest, prevState string) {
	switch pr.State {
	case "open", "draft":
		go h.refreshVCSMergeability(conn, pr, vcsMergeabilitySettleDelay)
	case "merged":
		if prevState == "merged" {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), vcsMergeabilityReadTimeout)
			defer cancel()
			others, err := h.Queries.ListOpenLinkedVCSPullRequestsInRepo(ctx, db.ListOpenLinkedVCSPullRequestsInRepoParams{
				ConnectionID: conn.ID,
				RepoOwner:    pr.RepoOwner,
				RepoName:     pr.RepoName,
				ID:           pr.ID,
			})
			if err != nil {
				slog.Warn("vcs: list pull requests to re-read after a merge failed", "err", err)
				return
			}
			for _, other := range others {
				go h.refreshVCSMergeability(conn, other, vcsMergeabilitySettleDelay)
			}
		}()
	}
}

// refreshVCSMergeability reads one pull request's verdict and stores it. It
// runs on its own: the webhook that caused it has long been answered.
func (h *Handler) refreshVCSMergeability(conn db.VcsConnection, pr db.VcsPullRequest, delay time.Duration) {
	if !h.isVCSConfigured() {
		return
	}
	if _, busy := vcsMergeabilityInflight.LoadOrStore(pr.ID, struct{}{}); busy {
		return
	}
	defer vcsMergeabilityInflight.Delete(pr.ID)
	time.Sleep(delay)

	ctx, cancel := context.WithTimeout(context.Background(), 2*vcsMergeabilityReadTimeout+vcsMergeabilityConfirmDelay)
	defer cancel()

	// Only pull requests an issue is linked to are worth a request; the link
	// is written by the same event, just before this runs.
	issueIDs, err := h.Queries.ListIssueIDsForVCSPullRequest(ctx, pr.ID)
	if err != nil || len(issueIDs) == 0 {
		return
	}
	token, err := h.openVCSSecret(conn.AccessTokenEncrypted)
	if err != nil {
		slog.Warn("vcs: decrypt access token failed", "err", err)
		return
	}
	verdict, err := settleVCSMergeability(
		func() (vcs.Mergeability, error) {
			readCtx, cancelRead := context.WithTimeout(ctx, vcsMergeabilityReadTimeout)
			defer cancelRead()
			return vcsReadMergeability(readCtx, conn, token, pr)
		},
		func() { time.Sleep(vcsMergeabilityConfirmDelay) },
	)
	if err != nil {
		slog.Warn("vcs: read pull request mergeability failed",
			"provider", conn.Provider, "repo", pr.RepoOwner+"/"+pr.RepoName, "number", pr.PrNumber, "err", err)
		return
	}
	head := verdict.HeadSHA
	if head == "" {
		head = pr.HeadSha
	}

	previous, err := h.Queries.GetVCSPullRequestMergeability(ctx, pr.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("vcs: load pull request mergeability failed", "err", err)
		return
	}
	if err := h.Queries.UpsertVCSPullRequestMergeability(ctx, db.UpsertVCSPullRequestMergeabilityParams{
		PullRequestID: pr.ID,
		HeadSha:       head,
		State:         string(verdict.State),
	}); err != nil {
		slog.Warn("vcs: store pull request mergeability failed", "err", err)
		return
	}
	if previous.State == string(verdict.State) && previous.HeadSha == head {
		return
	}
	workspaceID := uuidToString(conn.WorkspaceID)
	for _, issueID := range issueIDs {
		h.publish(protocol.EventPullRequestUpdated, workspaceID, "system", "", map[string]any{
			"issue_id": uuidToString(issueID),
		})
	}
}

// SweepVCSMergeability re-reads the watched pull requests whose verdict has
// aged, and drops verdicts whose pull request is gone.
func (h *Handler) SweepVCSMergeability(ctx context.Context) {
	if !h.isVCSAvailable() || !h.isVCSConfigured() {
		return
	}
	if _, err := h.Queries.DeleteOrphanVCSPullRequestMergeability(ctx); err != nil {
		slog.Warn("vcs: drop orphan mergeability rows failed", "err", err)
	}
	stale, err := h.Queries.ListStaleConflictWatchedVCSPullRequests(ctx, db.ListStaleConflictWatchedVCSPullRequestsParams{
		OlderThan: pgtype.Timestamptz{Time: time.Now().Add(-vcsMergeabilityMaxAge), Valid: true},
		MaxRows:   vcsMergeabilitySweepBatch,
	})
	if err != nil {
		slog.Warn("vcs: list stale mergeability rows failed", "err", err)
		return
	}
	connections := make(map[pgtype.UUID]db.VcsConnection)
	for _, pr := range stale {
		conn, ok := connections[pr.ConnectionID]
		if !ok {
			conn, err = h.Queries.GetVCSConnectionByID(ctx, pr.ConnectionID)
			if err != nil {
				continue
			}
			connections[pr.ConnectionID] = conn
		}
		go h.refreshVCSMergeability(conn, pr, 0)
	}
}

// vcsMergeabilityByPullRequest returns the verdict to show on each pull
// request card: the stored one, as long as it is for the head the card shows.
func (h *Handler) vcsMergeabilityByPullRequest(ctx context.Context, rows []db.ListVCSPullRequestsByIssueRow) map[pgtype.UUID]*string {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	heads := make(map[pgtype.UUID]string, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		heads[row.ID] = row.HeadSha
	}
	verdicts, err := h.Queries.ListVCSPullRequestMergeability(ctx, ids)
	if err != nil {
		slog.Warn("vcs: load pull request mergeability failed", "err", err)
		return nil
	}
	out := make(map[pgtype.UUID]*string, len(verdicts))
	for _, v := range verdicts {
		if v.HeadSha != heads[v.PullRequestID] {
			continue
		}
		state := v.State
		out[v.PullRequestID] = &state
	}
	return out
}
