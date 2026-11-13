package main

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/cli"
)

// The install scripts are compiled into the server, so they come from the same
// build as the CLI they download.
var (
	//go:embed cliinstall/install.sh
	cliInstallShell []byte
	//go:embed cliinstall/install.ps1
	cliInstallPowerShell []byte
)

// cliReleaseBinaryPath locates the multica binary for one platform from the
// same build as the server. Tests replace it.
var cliReleaseBinaryPath = defaultCLIReleaseBinaryPath

// The image keeps each platform's CLI under cli/<goos>-<goarch>/ next to the
// server. The server's own platform falls back to the multica beside it when
// that directory is missing, which is how `make build` lays its output out.
func defaultCLIReleaseBinaryPath(goos, goarch string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	name := "multica"
	if goos == "windows" {
		name = "multica.exe"
	}
	path := filepath.Join(dir, "cli", goos+"-"+goarch, name)
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return filepath.Join(dir, name), nil
		}
	}
	return path, nil
}

type packedServerCLI struct {
	key     string
	release cli.ServerCLIRelease
	archive []byte
}

var (
	cliReleaseMu    sync.Mutex
	cliReleaseCache = map[string]*packedServerCLI{}
)

// A platform name becomes part of a file path, so only the characters Go
// platform names use are accepted.
var cliPlatformPart = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// cliPlatformFromRequest reads the platform a request asks for: path
// parameters first, then query parameters, and the server's own when neither
// is given.
func cliPlatformFromRequest(r *http.Request) (goos, goarch string, ok bool) {
	goos, goarch = chi.URLParam(r, "goos"), chi.URLParam(r, "goarch")
	if goos == "" && goarch == "" {
		goos, goarch = r.URL.Query().Get("goos"), r.URL.Query().Get("goarch")
	}
	if goos == "" && goarch == "" {
		return runtime.GOOS, runtime.GOARCH, true
	}
	if !cliPlatformPart.MatchString(goos) || !cliPlatformPart.MatchString(goarch) {
		return "", "", false
	}
	return goos, goarch, true
}

func loadServerCLIRelease(goos, goarch string) (*packedServerCLI, error) {
	path, err := cliReleaseBinaryPath(goos, goarch)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	platform := goos + "/" + goarch
	key := fmt.Sprintf("%s:%d:%d", path, info.ModTime().UnixNano(), info.Size())
	cliReleaseMu.Lock()
	defer cliReleaseMu.Unlock()
	if cached := cliReleaseCache[platform]; cached != nil && cached.key == key && cached.release.Commit == commit && cached.release.Version == version {
		return cached, nil
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name, archive, sum, err := cli.BuildCLIArchive(binary, version, goos, goarch)
	if err != nil {
		return nil, err
	}
	packed := &packedServerCLI{
		key: key,
		release: cli.ServerCLIRelease{
			Version:     version,
			Commit:      commit,
			GOOS:        goos,
			GOARCH:      goarch,
			ArchiveName: name,
			SHA256:      sum,
		},
		archive: archive,
	}
	cliReleaseCache[platform] = packed
	return packed, nil
}

// loadRequestedCLIRelease resolves the requested platform and loads its
// archive. On failure it has already written the response.
func loadRequestedCLIRelease(w http.ResponseWriter, r *http.Request) (*packedServerCLI, bool) {
	goos, goarch, ok := cliPlatformFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid goos or goarch"})
		return nil, false
	}
	packed, err := loadServerCLIRelease(goos, goarch)
	if err != nil {
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "cli release is not available"})
		return nil, false
	}
	return packed, true
}

func handleCLIRelease(w http.ResponseWriter, r *http.Request) {
	packed, ok := loadRequestedCLIRelease(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, packed.release)
}

func handleCLIReleaseArchive(w http.ResponseWriter, r *http.Request) {
	packed, ok := loadRequestedCLIRelease(w, r)
	if !ok {
		return
	}
	contentType := "application/gzip"
	if packed.release.GOOS == "windows" {
		contentType = "application/zip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, packed.release.ArchiveName))
	w.Header().Set("X-Checksum-Sha256", packed.release.SHA256)
	w.Header().Set("X-Multica-Version", packed.release.Version)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packed.archive)
}

func handleCLIInstallShell(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(cliInstallShell)
}

func handleCLIInstallPowerShell(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(cliInstallPowerShell)
}
