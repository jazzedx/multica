package main

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const (
	builtinCodeHostStartupRetry  = 15 * time.Second
	builtinCodeHostSweepInterval = 5 * time.Minute
)

// runBuiltinCodeHostSweeper keeps every workspace linked to the Gitea deployed
// beside this server. Creating a workspace and repository webhooks trigger the
// same work right away; the sweep covers what those miss: workspaces that
// existed before the add-on was turned on, a service token that arrives after
// startup, and a lost webhook delivery.
func runBuiltinCodeHostSweeper(ctx context.Context, h *handler.Handler) {
	// The first pass also rewrites existing webhooks, repairing a secret that
	// drifted while the server was down.
	refresh := true
	timer := time.NewTimer(builtinCodeHostStartupRetry)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			sweepCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			ready := h.ReconcileBuiltinCodeHost(sweepCtx, refresh)
			cancel()
			if !ready {
				timer.Reset(builtinCodeHostStartupRetry)
				continue
			}
			refresh = false
			timer.Reset(builtinCodeHostSweepInterval)
		}
	}
}
