package main

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const vcsMergeabilitySweepInterval = time.Minute

// runVCSMergeabilitySweeper keeps the merge-conflict verdict of watched
// Forgejo / Gitea / GitLab pull requests from going stale: a push to a base
// branch changes it without sending any event for the pull request.
func runVCSMergeabilitySweeper(ctx context.Context, h *handler.Handler) {
	ticker := time.NewTicker(vcsMergeabilitySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			h.SweepVCSMergeability(sweepCtx)
			cancel()
		}
	}
}
