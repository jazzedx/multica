package daemon

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/multica-ai/multica/server/internal/cli"
)

// fetchServerCLI reads the CLI from the server the daemon is logged in to.
// Tests replace it to stay off the network.
var fetchServerCLI = func(ctx context.Context, d *Daemon) (*cli.ServerCLIRelease, []byte, error) {
	if d.client == nil {
		return nil, nil, fmt.Errorf("daemon has no API client")
	}
	rel, err := cli.FetchServerCLIMetadata(ctx, d.cfg.ServerBaseURL, d.client.token)
	if err != nil {
		return nil, nil, err
	}
	if rel == nil || rel.Commit == d.cfg.CLIShortCommit {
		return rel, nil, nil
	}
	archive, err := cli.DownloadServerCLIArchive(ctx, d.cfg.ServerBaseURL, d.client.token)
	if err != nil {
		return rel, nil, err
	}
	return rel, archive, nil
}

// installServerCLI verifies the archive and replaces the running executable.
var installServerCLI = cli.InstallVerifiedArchive

func (d *Daemon) serverCLIFollowEnabled() bool {
	if !d.cfg.FollowServerCLI || d.cfg.LaunchedBy == "desktop" {
		return false
	}
	if IsOfficialCloudServer(d.cfg.ServerBaseURL) {
		return false
	}
	return cli.FollowableCommit(d.cfg.CLIShortCommit)
}

// tryFollowServerCLI downloads the CLI from the self-hosted server's build and
// restarts when that build's commit differs from this process.
// It waits for the next round while tasks are running, and replaces nothing when
// verification fails.
func (d *Daemon) tryFollowServerCLI(ctx context.Context) {
	if ctx.Err() != nil || !d.serverCLIFollowEnabled() {
		return
	}
	if d.updating.Load() || d.activeTasks.Load() > 0 {
		return
	}
	rel, archive, err := fetchServerCLI(ctx, d)
	if err != nil {
		var reqErr *requestError
		if errors.As(err, &reqErr) && reqErr.StatusCode == 404 {
			d.logger.Debug("server cli: this server does not publish a cli")
			return
		}
		var httpErr *cli.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 404 {
			d.logger.Debug("server cli: this server does not publish a cli")
			return
		}
		d.logger.Warn("server cli: fetch failed — will retry", "error", err)
		return
	}
	if rel == nil || !cli.FollowableCommit(rel.Commit) || rel.Commit == d.cfg.CLIShortCommit {
		return
	}
	if rel.GOOS != "" && rel.GOOS != runtime.GOOS || rel.GOARCH != "" && rel.GOARCH != runtime.GOARCH {
		d.logger.Info("server cli: architecture differs, leaving the local binary in place",
			"server", rel.GOOS+"/"+rel.GOARCH, "local", runtime.GOOS+"/"+runtime.GOARCH)
		return
	}
	if !d.updating.CompareAndSwap(false, true) {
		return
	}
	released := false
	defer func() {
		if !released {
			d.updating.Store(false)
		}
	}()
	if !d.trySetClaimBarrier() {
		d.logger.Info("server cli: deferring — task or claim in flight")
		return
	}
	barrierReleased := false
	defer func() {
		if !barrierReleased {
			d.releaseClaimBarrier()
		}
	}()
	if _, err := installServerCLI(archive, rel.SHA256); err != nil {
		d.logger.Warn("server cli: install failed — will retry", "error", err)
		return
	}
	d.logger.Info("server cli: installed the server build, restarting", "commit", rel.Commit, "version", rel.Version)
	if !d.triggerRestart() {
		d.logger.Error("server cli: installed but restart could not be scheduled — resuming claims")
		return
	}
	released = true
	barrierReleased = true
}
