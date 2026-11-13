package daemon

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// serverCLIInstallTimeout bounds one download-and-install of the server's CLI.
const serverCLIInstallTimeout = 3 * time.Minute

// fetchServerCLIMetadata reads what the logged-in server ships for this
// platform, without the archive. Tests replace it to stay off the network.
var fetchServerCLIMetadata = func(ctx context.Context, d *Daemon) (*cli.ServerCLIRelease, error) {
	if d.client == nil {
		return nil, fmt.Errorf("daemon has no API client")
	}
	return cli.FetchServerCLIMetadata(ctx, d.cfg.ServerBaseURL, d.client.token)
}

// fetchServerCLI reads the CLI from the server the daemon is logged in to, and
// its archive when the build differs from this process. Tests replace it to
// stay off the network.
var fetchServerCLI = func(ctx context.Context, d *Daemon) (*cli.ServerCLIRelease, []byte, error) {
	rel, err := fetchServerCLIMetadata(ctx, d)
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

// usesServerCLI reports whether this daemon takes its CLI from the server it is
// logged in to: a self-hosted server, and a daemon no launcher manages.
func (d *Daemon) usesServerCLI() bool {
	return d.cfg.LaunchedBy != "desktop" && !IsOfficialCloudServer(d.cfg.ServerBaseURL)
}

// serverCLIWatchEnabled reports whether the daemon compares its build with the
// server's at all. A build with no usable commit has nothing to compare.
func (d *Daemon) serverCLIWatchEnabled() bool {
	return d.usesServerCLI() && cli.FollowableCommit(d.cfg.CLIShortCommit)
}

// serverCLIFollowEnabled reports whether the daemon installs a differing
// server build on its own. Off unless the operator opts in; otherwise the
// daemon only reports the build (noteServerCLI).
func (d *Daemon) serverCLIFollowEnabled() bool {
	return d.cfg.FollowServerCLI && d.serverCLIWatchEnabled()
}

// serverCLIMismatch describes why this machine cannot run rel, or "" when it can.
func serverCLIMismatch(rel *cli.ServerCLIRelease) string {
	if rel.GOOS != "" && rel.GOOS != runtime.GOOS || rel.GOARCH != "" && rel.GOARCH != runtime.GOARCH {
		return fmt.Sprintf("the server's CLI is built for %s/%s, this machine is %s/%s",
			rel.GOOS, rel.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	return ""
}

// serverPublishesNoCLI reports whether err means the server has no CLI to hand
// out, as opposed to a failure worth retrying.
func serverPublishesNoCLI(err error) bool {
	var reqErr *requestError
	if errors.As(err, &reqErr) && reqErr.StatusCode == 404 {
		return true
	}
	var httpErr *cli.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == 404
}

// serverCLIUpdateVersion is the version of a server build this daemon could
// move to, or "" when it already runs it or none is known.
func (d *Daemon) serverCLIUpdateVersion() string {
	if rel := d.serverCLIUpdate.Load(); rel != nil {
		return rel.Version
	}
	return ""
}

// noteServerCLI records whether the server ships a build this daemon could move
// to, and says so once per build. It installs nothing: the update is started
// from the Runtimes page or with `multica update`.
func (d *Daemon) noteServerCLI(ctx context.Context) {
	if ctx.Err() != nil || !d.serverCLIWatchEnabled() {
		return
	}
	rel, err := fetchServerCLIMetadata(ctx, d)
	if err != nil {
		if !serverPublishesNoCLI(err) {
			d.logger.Debug("server cli: could not read the server's build", "error", err)
		}
		return
	}
	if rel == nil || !cli.FollowableCommit(rel.Commit) || rel.Commit == d.cfg.CLIShortCommit || serverCLIMismatch(rel) != "" {
		d.serverCLIUpdate.Store(nil)
		return
	}
	if prev := d.serverCLIUpdate.Swap(rel); prev == nil || prev.Commit != rel.Commit {
		d.logger.Info("server cli: the server ships a different build; update from the Runtimes page or run 'multica update'",
			"version", rel.Version, "commit", rel.Commit, "running", d.cfg.CLIVersion)
	}
}

// updateFromServer installs the CLI the logged-in server ships and returns a
// line describing what happened. The caller restarts the daemon.
func (d *Daemon) updateFromServer() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), serverCLIInstallTimeout)
	defer cancel()

	rel, archive, err := fetchServerCLI(ctx, d)
	if err != nil {
		if serverPublishesNoCLI(err) {
			return "", errors.New("this server does not publish a CLI")
		}
		return "", fmt.Errorf("fetch the server's CLI: %w", err)
	}
	if rel == nil {
		return "", errors.New("this server does not publish a CLI")
	}
	if archive == nil {
		return fmt.Sprintf("Already on the server's build %s", rel.Version), nil
	}
	if mismatch := serverCLIMismatch(rel); mismatch != "" {
		return "", errors.New(mismatch)
	}
	if _, err := installServerCLI(archive, rel.SHA256); err != nil {
		return "", fmt.Errorf("install the server's CLI: %w", err)
	}
	d.serverCLIUpdate.Store(nil)
	return fmt.Sprintf("Installed %s (%s) from the server", rel.Version, rel.Commit), nil
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
		if serverPublishesNoCLI(err) {
			d.logger.Debug("server cli: this server does not publish a cli")
			return
		}
		d.logger.Warn("server cli: fetch failed — will retry", "error", err)
		return
	}
	if rel == nil || !cli.FollowableCommit(rel.Commit) || rel.Commit == d.cfg.CLIShortCommit {
		return
	}
	if mismatch := serverCLIMismatch(rel); mismatch != "" {
		d.logger.Info("server cli: leaving the local binary in place", "reason", mismatch)
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
