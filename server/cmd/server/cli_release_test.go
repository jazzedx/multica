package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/cli"
)

func resetCLIReleaseCache(t *testing.T) {
	t.Helper()
	reset := func() {
		cliReleaseMu.Lock()
		cliReleaseCache = map[string]*packedServerCLI{}
		cliReleaseMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// stubCLIBinaries gives each platform a fake binary with its own content.
func stubCLIBinaries(t *testing.T, platforms ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, platform := range platforms {
		if err := os.WriteFile(filepath.Join(dir, platform), []byte("cli-for-"+platform), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := cliReleaseBinaryPath
	cliReleaseBinaryPath = func(goos, goarch string) (string, error) {
		return filepath.Join(dir, goos+"-"+goarch), nil
	}
	t.Cleanup(func() { cliReleaseBinaryPath = orig })
}

func stubBuildInfo(t *testing.T) {
	t.Helper()
	origCommit, origVersion := commit, version
	commit, version = "abc1234", "v0.9.0-1-gabc1234"
	t.Cleanup(func() { commit, version = origCommit, origVersion })
}

// cliRouter mounts only the CLI distribution routes, at the paths router.go uses.
func cliRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/cli/install.sh", handleCLIInstallShell)
	r.Get("/api/cli/install.ps1", handleCLIInstallPowerShell)
	r.Get("/api/cli/download/{goos}/{goarch}", handleCLIReleaseArchive)
	r.Get("/api/cli/release", handleCLIRelease)
	r.Get("/api/cli/release/archive", handleCLIReleaseArchive)
	return r
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	cliRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestCLIReleaseDefaultsToTheServerPlatform(t *testing.T) {
	resetCLIReleaseCache(t)
	stubBuildInfo(t)
	stubCLIBinaries(t, runtime.GOOS+"-"+runtime.GOARCH)

	rec := get(t, "/api/cli/release")
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, body %s", rec.Code, rec.Body.Bytes())
	}
	var rel cli.ServerCLIRelease
	if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}
	if rel.Commit != "abc1234" || rel.Version != "v0.9.0-1-gabc1234" || rel.SHA256 == "" {
		t.Fatalf("release = %+v", rel)
	}
	if rel.GOOS != runtime.GOOS || rel.GOARCH != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want the server's own", rel.GOOS, rel.GOARCH)
	}

	body := get(t, "/api/cli/release/archive")
	if body.Code != http.StatusOK {
		t.Fatalf("archive status = %d", body.Code)
	}
	if got := sha256Hex(body.Body.Bytes()); got != rel.SHA256 {
		t.Fatalf("archive sha = %s, metadata %s", got, rel.SHA256)
	}
}

func TestCLIReleaseServesTheRequestedPlatform(t *testing.T) {
	resetCLIReleaseCache(t)
	stubBuildInfo(t)
	stubCLIBinaries(t, "linux-amd64", "darwin-arm64")

	rec := get(t, "/api/cli/release?goos=darwin&goarch=arm64")
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, body %s", rec.Code, rec.Body.Bytes())
	}
	var rel cli.ServerCLIRelease
	if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}
	if rel.GOOS != "darwin" || rel.GOARCH != "arm64" {
		t.Fatalf("platform = %s/%s, want darwin/arm64", rel.GOOS, rel.GOARCH)
	}

	// The public download path serves the same archive as the authenticated one.
	download := get(t, "/api/cli/download/darwin/arm64")
	if download.Code != http.StatusOK {
		t.Fatalf("download status = %d", download.Code)
	}
	if got := sha256Hex(download.Body.Bytes()); got != rel.SHA256 {
		t.Fatalf("download sha = %s, metadata %s", got, rel.SHA256)
	}
	if got := download.Header().Get("X-Checksum-Sha256"); got != rel.SHA256 {
		t.Fatalf("checksum header = %q, want %s", got, rel.SHA256)
	}
	if got := download.Header().Get("X-Multica-Version"); got != "v0.9.0-1-gabc1234" {
		t.Fatalf("version header = %q", got)
	}

	other := get(t, "/api/cli/download/linux/amd64")
	if other.Code != http.StatusOK {
		t.Fatalf("linux download status = %d", other.Code)
	}
	if sha256Hex(other.Body.Bytes()) == rel.SHA256 {
		t.Fatal("linux/amd64 and darwin/arm64 must not share an archive")
	}
}

func TestCLIDownloadPacksWindowsAsZip(t *testing.T) {
	resetCLIReleaseCache(t)
	stubBuildInfo(t)
	stubCLIBinaries(t, "windows-amd64")

	rec := get(t, "/api/cli/download/windows/amd64")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.Bytes())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type = %q, want application/zip", got)
	}
	data := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "multica.exe" {
		t.Fatalf("zip entries = %v, want only multica.exe", zr.File)
	}
	entry, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	content, err := io.ReadAll(entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "cli-for-windows-amd64" {
		t.Fatalf("multica.exe = %q", content)
	}
}

func TestCLIReleaseMissingBinaryIsNotFound(t *testing.T) {
	resetCLIReleaseCache(t)
	stubCLIBinaries(t, "linux-amd64")

	for _, path := range []string{
		"/api/cli/release?goos=darwin&goarch=arm64",
		"/api/cli/download/darwin/arm64",
	} {
		if rec := get(t, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestCLIReleaseRejectsMalformedPlatform(t *testing.T) {
	resetCLIReleaseCache(t)
	called := false
	orig := cliReleaseBinaryPath
	cliReleaseBinaryPath = func(string, string) (string, error) {
		called = true
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { cliReleaseBinaryPath = orig })

	for _, path := range []string{
		"/api/cli/release?goos=..&goarch=amd64",
		"/api/cli/release?goos=linux",
		"/api/cli/download/linux/..%2F..%2Fserver",
		"/api/cli/download/LINUX/amd64",
	} {
		if rec := get(t, path); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, rec.Code)
		}
	}
	if called {
		t.Fatal("a malformed platform must not reach the file system")
	}
}

func TestCLIInstallScriptsDownloadFromTheServerTheyCameFrom(t *testing.T) {
	shell := get(t, "/api/cli/install.sh")
	if shell.Code != http.StatusOK {
		t.Fatalf("install.sh status = %d", shell.Code)
	}
	if !strings.HasPrefix(shell.Body.String(), "#!/usr/bin/env bash") {
		t.Fatal("install.sh must start with a bash shebang")
	}
	if !strings.Contains(shell.Body.String(), `$server_url/api/cli/download/$goos/$goarch`) {
		t.Fatal("install.sh must download from the public download route")
	}

	powershell := get(t, "/api/cli/install.ps1")
	if powershell.Code != http.StatusOK {
		t.Fatalf("install.ps1 status = %d", powershell.Code)
	}
	if !strings.Contains(powershell.Body.String(), `$serverUrl/api/cli/download/windows/$arch`) {
		t.Fatal("install.ps1 must download from the public download route")
	}
}
