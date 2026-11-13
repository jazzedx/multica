package daemon

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestTryFollowServerCLIInstallsWhenCommitDiffers(t *testing.T) {
	d, restarts := newSelfReloadTestDaemon(t, "v0.9.0-1-gold")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.FollowServerCLI = true
	d.cfg.CLIShortCommit = "oldcommit"

	origFetch, origInstall := fetchServerCLI, installServerCLI
	t.Cleanup(func() {
		fetchServerCLI = origFetch
		installServerCLI = origInstall
	})
	var installed atomic.Int32
	fetchServerCLI = func(context.Context, *Daemon) (*cli.ServerCLIRelease, []byte, error) {
		return &cli.ServerCLIRelease{
			Version: "v0.9.0-1-gnew",
			Commit:  "newcommit",
			GOOS:    runtime.GOOS,
			GOARCH:  runtime.GOARCH,
			SHA256:  "abc",
		}, []byte("archive"), nil
	}
	installServerCLI = func(archive []byte, sum string) (string, error) {
		installed.Add(1)
		if string(archive) != "archive" || sum != "abc" {
			t.Fatalf("install args archive=%q sum=%s", archive, sum)
		}
		return "/usr/local/bin/multica", nil
	}

	d.tryFollowServerCLI(context.Background())

	if installed.Load() != 1 {
		t.Fatalf("install calls = %d, want 1", installed.Load())
	}
	if restarts.Load() != 1 {
		t.Fatalf("restarts = %d, want 1", restarts.Load())
	}
}

func TestTryFollowServerCLISkipsMatchingCommit(t *testing.T) {
	d, restarts := newSelfReloadTestDaemon(t, "v0.9.0-1-gsame")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.FollowServerCLI = true
	d.cfg.CLIShortCommit = "same"

	origFetch, origInstall := fetchServerCLI, installServerCLI
	t.Cleanup(func() {
		fetchServerCLI = origFetch
		installServerCLI = origInstall
	})
	fetchServerCLI = func(context.Context, *Daemon) (*cli.ServerCLIRelease, []byte, error) {
		return &cli.ServerCLIRelease{Commit: "same", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, []byte("archive"), nil
	}
	installServerCLI = func([]byte, string) (string, error) {
		t.Fatal("matching commit must not install")
		return "", nil
	}

	d.tryFollowServerCLI(context.Background())
	if restarts.Load() != 0 {
		t.Fatalf("restarts = %d, want 0", restarts.Load())
	}
}

func TestTryFollowServerCLISkipsOfficialCloud(t *testing.T) {
	d, _ := newSelfReloadTestDaemon(t, "v0.9.0")
	d.cfg.ServerBaseURL = "https://api.multica.ai"
	d.cfg.FollowServerCLI = true
	d.cfg.CLIShortCommit = "abc"

	origFetch := fetchServerCLI
	t.Cleanup(func() { fetchServerCLI = origFetch })
	fetchServerCLI = func(context.Context, *Daemon) (*cli.ServerCLIRelease, []byte, error) {
		t.Fatal("official cloud must not fetch a self-hosted cli")
		return nil, nil, nil
	}
	d.tryFollowServerCLI(context.Background())
}
