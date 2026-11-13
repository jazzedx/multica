package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/selfexec"
)

// MaxCLIArchiveBytes caps a single CLI archive. A real multica binary is far
// smaller.
const MaxCLIArchiveBytes = 80 << 20

// ServerCLIRelease describes the CLI a self-hosted server ships for its build.
type ServerCLIRelease struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	ArchiveName string `json:"archive_name"`
	SHA256      string `json:"sha256"`
}

// FollowableCommit reports whether a commit can be compared with the server's.
func FollowableCommit(commit string) bool {
	switch strings.TrimSpace(commit) {
	case "", "unknown", "dev":
		return false
	default:
		return true
	}
}

// BuildCLIArchive packs a CLI binary into a tar.gz whose entry is named multica,
// matching what the direct-download update unpacks. sum is the SHA-256 of the
// archive bytes.
func BuildCLIArchive(binary []byte, version, goos, goarch string) (name string, archive []byte, sum string, err error) {
	if len(binary) == 0 {
		return "", nil, "", fmt.Errorf("cli binary is empty")
	}
	if goos == "windows" {
		return "", nil, "", fmt.Errorf("windows cli archive is not produced here")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	name = fmt.Sprintf("multica-cli-%s-%s-%s.tar.gz", strings.TrimPrefix(version, "v"), goos, goarch)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:    "multica",
		Mode:    0o755,
		Size:    int64(len(binary)),
		ModTime: time.Unix(0, 0),
	}); err != nil {
		return "", nil, "", err
	}
	if _, err := tw.Write(binary); err != nil {
		return "", nil, "", err
	}
	if err := tw.Close(); err != nil {
		return "", nil, "", err
	}
	if err := gz.Close(); err != nil {
		return "", nil, "", err
	}
	archive = buf.Bytes()
	digest := sha256.Sum256(archive)
	return name, archive, hex.EncodeToString(digest[:]), nil
}

// resolveInstallTarget resolves the running executable that gets replaced.
// Tests replace it.
var resolveInstallTarget = selfexec.Resolve

// InstallVerifiedArchive checks the archive's SHA-256, extracts multica from it
// and atomically replaces the running executable.
func InstallVerifiedArchive(archive []byte, expectedSHA string) (string, error) {
	if len(archive) == 0 || len(archive) > MaxCLIArchiveBytes {
		return "", fmt.Errorf("cli archive size %d is outside 1..%d", len(archive), MaxCLIArchiveBytes)
	}
	if err := verifyAssetSHA256(archive, expectedSHA, "cli archive"); err != nil {
		return "", err
	}
	binary, err := extractBinaryFromTarGz(bytes.NewReader(archive), "multica")
	if err != nil {
		return "", err
	}
	exePath, err := resolveInstallTarget()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve symlink: %w", err)
	}
	dir := filepath.Dir(exePath)
	tmpFile, err := os.CreateTemp(dir, "multica-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(binary); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	info, err := os.Stat(exePath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("stat original binary: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err := replaceBinary(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("replace binary: %w", err)
	}
	return exePath, nil
}

// FetchServerCLIMetadata reads a self-hosted server's CLI description without
// downloading the archive.
func FetchServerCLIMetadata(ctx context.Context, baseURL, token string) (*ServerCLIRelease, error) {
	client := NewAPIClient(baseURL, "", token)
	var rel ServerCLIRelease
	if err := client.GetJSON(ctx, "/api/cli/release", &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// DownloadServerCLIArchive downloads the archive that description refers to.
func DownloadServerCLIArchive(ctx context.Context, baseURL, token string) ([]byte, error) {
	client := NewAPIClient(baseURL, "", token)
	client.HTTPClient.Timeout = 2 * time.Minute
	return client.GetBytes(ctx, "/api/cli/release/archive", MaxCLIArchiveBytes)
}

// ApplyServerCLI installs the server's CLI when the commits differ and leaves
// the file alone when they match.
func ApplyServerCLI(localCommit string, rel *ServerCLIRelease, archive []byte) (bool, error) {
	if rel == nil {
		return false, fmt.Errorf("missing cli release")
	}
	if !FollowableCommit(rel.Commit) {
		return false, fmt.Errorf("server cli commit %q is not followable", rel.Commit)
	}
	if rel.Commit == strings.TrimSpace(localCommit) {
		return false, nil
	}
	if rel.GOOS != "" && rel.GOOS != runtime.GOOS || rel.GOARCH != "" && rel.GOARCH != runtime.GOARCH {
		return false, fmt.Errorf("server cli is %s/%s, this machine is %s/%s", rel.GOOS, rel.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if _, err := InstallVerifiedArchive(archive, rel.SHA256); err != nil {
		return false, err
	}
	return true, nil
}
