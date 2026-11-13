package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyServerCLIReplacesBinaryWhenCommitDiffers(t *testing.T) {
	target := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(target, []byte("old-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := resolveInstallTarget
	resolveInstallTarget = func() (string, error) { return target, nil }
	t.Cleanup(func() { resolveInstallTarget = orig })

	_, archive, sum, err := BuildCLIArchive([]byte("new-cli"), "v0.9.0-1-gabc", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyServerCLI("oldcommit", &ServerCLIRelease{
		Commit: "newcommit",
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		SHA256: sum,
	}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("expected the binary to be replaced")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-cli" {
		t.Fatalf("binary = %q, want new-cli", got)
	}
}

func TestApplyServerCLILeavesBinaryWhenCommitMatches(t *testing.T) {
	target := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(target, []byte("old-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := resolveInstallTarget
	resolveInstallTarget = func() (string, error) { return target, nil }
	t.Cleanup(func() { resolveInstallTarget = orig })

	updated, err := ApplyServerCLI("same", &ServerCLIRelease{
		Commit: "same",
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		SHA256: "unused",
	}, []byte("not-an-archive"))
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("matching commit must not replace the binary")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-cli" {
		t.Fatalf("binary = %q, want old-cli", got)
	}
}

func TestInstallVerifiedArchiveRejectsChecksumMismatch(t *testing.T) {
	target := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(target, []byte("old-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := resolveInstallTarget
	resolveInstallTarget = func() (string, error) { return target, nil }
	t.Cleanup(func() { resolveInstallTarget = orig })

	_, archive, _, err := BuildCLIArchive([]byte("new-cli"), "v0.9.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallVerifiedArchive(archive, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("checksum mismatch must fail")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-cli" {
		t.Fatalf("binary = %q, want old-cli", got)
	}
}

// The server packs each platform the way that platform unpacks it: a zip
// holding multica.exe for Windows, a tar.gz holding multica elsewhere.
func TestExtractServerCLIReadsEachPlatformsArchive(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		_, archive, _, err := BuildCLIArchive([]byte("cli-for-"+goos), "v0.9.0", goos, "amd64")
		if err != nil {
			t.Fatal(err)
		}
		binary, err := extractServerCLI(archive, goos)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if string(binary) != "cli-for-"+goos {
			t.Fatalf("%s: binary = %q", goos, binary)
		}
	}
}

func TestExtractServerCLIRejectsAnotherPlatformsArchive(t *testing.T) {
	_, archive, _, err := BuildCLIArchive([]byte("cli"), "v0.9.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extractServerCLI(archive, "windows"); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("a tar.gz handed to Windows must fail as a bad zip, got %v", err)
	}
}
