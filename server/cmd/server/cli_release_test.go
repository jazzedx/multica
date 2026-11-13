package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestCLIReleaseServesTheBinaryNextToTheServer(t *testing.T) {
	cliReleaseMu.Lock()
	cliReleaseCache = nil
	cliReleaseMu.Unlock()
	t.Cleanup(func() {
		cliReleaseMu.Lock()
		cliReleaseCache = nil
		cliReleaseMu.Unlock()
	})

	origCommit, origVersion := commit, version
	commit, version = "abc1234", "v0.9.0-1-gabc1234"
	t.Cleanup(func() {
		commit, version = origCommit, origVersion
	})

	path := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(path, []byte("packaged-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	origPath := cliReleaseBinaryPath
	cliReleaseBinaryPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { cliReleaseBinaryPath = origPath })

	rec := httptest.NewRecorder()
	handleCLIRelease(rec, httptest.NewRequest(http.MethodGet, "/api/cli/release", nil))
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

	body := httptest.NewRecorder()
	handleCLIReleaseArchive(body, httptest.NewRequest(http.MethodGet, "/api/cli/release/archive", nil))
	if body.Code != http.StatusOK {
		t.Fatalf("archive status = %d", body.Code)
	}
	digest := sha256.Sum256(body.Body.Bytes())
	if hex.EncodeToString(digest[:]) != rel.SHA256 {
		t.Fatalf("archive sha = %s, metadata %s", hex.EncodeToString(digest[:]), rel.SHA256)
	}
}

func TestCLIReleaseMissingBinaryIsNotFound(t *testing.T) {
	cliReleaseMu.Lock()
	cliReleaseCache = nil
	cliReleaseMu.Unlock()
	t.Cleanup(func() {
		cliReleaseMu.Lock()
		cliReleaseCache = nil
		cliReleaseMu.Unlock()
	})
	origPath := cliReleaseBinaryPath
	cliReleaseBinaryPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "missing"), nil
	}
	t.Cleanup(func() { cliReleaseBinaryPath = origPath })

	rec := httptest.NewRecorder()
	handleCLIRelease(rec, httptest.NewRequest(http.MethodGet, "/api/cli/release", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
