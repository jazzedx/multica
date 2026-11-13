package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/multica-ai/multica/server/internal/cli"
)

// cliReleaseBinaryPath locates the multica binary from the same build as the
// server. Tests replace it.
var cliReleaseBinaryPath = defaultCLIReleaseBinaryPath

func defaultCLIReleaseBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "multica"), nil
}

type packedServerCLI struct {
	key     string
	release cli.ServerCLIRelease
	archive []byte
}

var (
	cliReleaseMu    sync.Mutex
	cliReleaseCache *packedServerCLI
)

func loadServerCLIRelease() (*packedServerCLI, error) {
	path, err := cliReleaseBinaryPath()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s:%d:%d", path, info.ModTime().UnixNano(), info.Size())
	cliReleaseMu.Lock()
	defer cliReleaseMu.Unlock()
	if cliReleaseCache != nil && cliReleaseCache.key == key && cliReleaseCache.release.Commit == commit && cliReleaseCache.release.Version == version {
		return cliReleaseCache, nil
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name, archive, sum, err := cli.BuildCLIArchive(binary, version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	packed := &packedServerCLI{
		key: key,
		release: cli.ServerCLIRelease{
			Version:     version,
			Commit:      commit,
			GOOS:        runtime.GOOS,
			GOARCH:      runtime.GOARCH,
			ArchiveName: name,
			SHA256:      sum,
		},
		archive: archive,
	}
	cliReleaseCache = packed
	return packed, nil
}

func handleCLIRelease(w http.ResponseWriter, _ *http.Request) {
	packed, err := loadServerCLIRelease()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "cli release is not available"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cli release is not available"})
		return
	}
	writeJSON(w, http.StatusOK, packed.release)
}

func handleCLIReleaseArchive(w http.ResponseWriter, _ *http.Request) {
	packed, err := loadServerCLIRelease()
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "cli release is not available", http.StatusNotFound)
			return
		}
		http.Error(w, "cli release is not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("X-Checksum-Sha256", packed.release.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packed.archive)
}
