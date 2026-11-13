package daemon

import (
	"context"
	"runtime"
	"strings"
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

func stubServerCLIMetadata(t *testing.T, rel *cli.ServerCLIRelease, err error) {
	t.Helper()
	orig := fetchServerCLIMetadata
	fetchServerCLIMetadata = func(context.Context, *Daemon) (*cli.ServerCLIRelease, error) { return rel, err }
	t.Cleanup(func() { fetchServerCLIMetadata = orig })
}

func refuseServerCLIInstall(t *testing.T) {
	t.Helper()
	orig := installServerCLI
	installServerCLI = func([]byte, string) (string, error) {
		t.Fatal("a daemon that only watches must not install")
		return "", nil
	}
	t.Cleanup(func() { installServerCLI = orig })
}

// With following off, a newer server build is recorded for `daemon status` and
// the log, and nothing on the machine changes.
func TestNoteServerCLIRecordsANewerBuildWithoutInstalling(t *testing.T) {
	d, restarts := newSelfReloadTestDaemon(t, "v0.9.0-1-gold")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.CLIShortCommit = "oldcommit"
	refuseServerCLIInstall(t)
	stubServerCLIMetadata(t, &cli.ServerCLIRelease{
		Version: "v0.9.0-2-gnew", Commit: "newcommit", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}, nil)

	d.noteServerCLI(context.Background())

	if got := d.serverCLIUpdateVersion(); got != "v0.9.0-2-gnew" {
		t.Fatalf("available update = %q, want v0.9.0-2-gnew", got)
	}
	if restarts.Load() != 0 {
		t.Fatalf("restarts = %d, want 0", restarts.Load())
	}

	// Once this daemon runs the server's build there is nothing left to offer.
	d.cfg.CLIShortCommit = "newcommit"
	d.noteServerCLI(context.Background())
	if got := d.serverCLIUpdateVersion(); got != "" {
		t.Fatalf("available update after catching up = %q, want none", got)
	}
}

func TestNoteServerCLIIgnoresABuildForAnotherPlatform(t *testing.T) {
	d, _ := newSelfReloadTestDaemon(t, "v0.9.0-1-gold")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.CLIShortCommit = "oldcommit"
	stubServerCLIMetadata(t, &cli.ServerCLIRelease{
		Version: "v0.9.0-2-gnew", Commit: "newcommit", GOOS: "plan9", GOARCH: runtime.GOARCH,
	}, nil)

	d.noteServerCLI(context.Background())

	if got := d.serverCLIUpdateVersion(); got != "" {
		t.Fatalf("available update = %q, want none for a build this machine cannot run", got)
	}
}

// An update requested from the web installs the build the server ships, not
// the public release.
func TestRunUpdateOnASelfHostedServerInstallsItsBuild(t *testing.T) {
	d, _ := newSelfReloadTestDaemon(t, "v0.9.0-1-gold")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.CLIShortCommit = "oldcommit"

	origFetch, origInstall := fetchServerCLI, installServerCLI
	t.Cleanup(func() {
		fetchServerCLI = origFetch
		installServerCLI = origInstall
	})
	fetchServerCLI = func(context.Context, *Daemon) (*cli.ServerCLIRelease, []byte, error) {
		return &cli.ServerCLIRelease{
			Version: "v0.9.0-2-gnew", Commit: "newcommit", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, SHA256: "abc",
		}, []byte("archive"), nil
	}
	var installed atomic.Int32
	installServerCLI = func(archive []byte, sum string) (string, error) {
		installed.Add(1)
		if string(archive) != "archive" || sum != "abc" {
			t.Fatalf("install args archive=%q sum=%s", archive, sum)
		}
		return "/usr/local/bin/multica", nil
	}

	output, err := d.runUpdate("v0.9.0-2-gnew")
	if err != nil {
		t.Fatal(err)
	}
	if installed.Load() != 1 {
		t.Fatalf("install calls = %d, want 1", installed.Load())
	}
	if !strings.Contains(output, "v0.9.0-2-gnew") {
		t.Fatalf("output = %q, want it to name the installed version", output)
	}
}

func TestRunUpdateOnASelfHostedServerRejectsABuildForAnotherPlatform(t *testing.T) {
	d, _ := newSelfReloadTestDaemon(t, "v0.9.0-1-gold")
	d.cfg.ServerBaseURL = "http://127.0.0.1:3000"
	d.cfg.CLIShortCommit = "oldcommit"
	refuseServerCLIInstall(t)

	origFetch := fetchServerCLI
	t.Cleanup(func() { fetchServerCLI = origFetch })
	fetchServerCLI = func(context.Context, *Daemon) (*cli.ServerCLIRelease, []byte, error) {
		return &cli.ServerCLIRelease{Commit: "newcommit", GOOS: "plan9", GOARCH: runtime.GOARCH}, []byte("archive"), nil
	}

	if _, err := d.runUpdate("v0.9.0-2-gnew"); err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("err = %v, want it to name the platform the server built for", err)
	}
}
