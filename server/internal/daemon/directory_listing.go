package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Directory listings come from a project's resources on the web: a member
// choosing a folder on this machine types a path, and the page asks which
// directories are there and whether the path can be a project's local
// directory. The server queues the question and sends a pending-work hint; the
// daemon claims it, reads the directory and reports what it found.

const (
	directoryListingMaxEntries = 2000
	directoryListingTimeout    = 8 * time.Second
)

// DirectoryListingRequest is one path the server wants read.
type DirectoryListingRequest struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// ProbeWrite also checks that the daemon can write inside the path, by
	// creating and removing a file there.
	ProbeWrite bool `json:"probe_write"`
}

// DirectoryListing is what was found at a path. It names directories only:
// no files, and nothing of their contents.
type DirectoryListing struct {
	Path      string   `json:"path"`
	Separator string   `json:"separator"`
	Entries   []string `json:"entries"`
	Truncated bool     `json:"truncated"`
	IsGitRepo bool     `json:"is_git_repo"`
	// Problem is why Path cannot be a project's local directory; empty when
	// nothing was found wrong.
	Problem string `json:"problem"`
	Detail  string `json:"detail"`
}

const (
	directoryProblemNotAbsolute  = "not_absolute"
	directoryProblemNotFound     = "not_found"
	directoryProblemNotDirectory = "not_a_directory"
	directoryProblemNotReadable  = "not_readable"
	directoryProblemNotWritable  = "not_writable"
	directoryProblemProtected    = "protected"
	directoryProblemError        = "error"
)

type directoryListingReport struct {
	Listing *DirectoryListing `json:"listing,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func (c *Client) ClaimDirectoryListings(ctx context.Context, runtimeID string) ([]DirectoryListingRequest, error) {
	var resp struct {
		Requests []DirectoryListingRequest `json:"requests"`
	}
	if err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/directories/claim", runtimeID), map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Requests, nil
}

func (c *Client) ReportDirectoryListing(ctx context.Context, runtimeID, requestID string, report directoryListingReport) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/directories/%s/result", runtimeID, requestID), report, nil)
}

// claimDirectoryListings answers the listings queued for a runtime. The server
// hands each request out once, so overlapping claims are harmless.
func (d *Daemon) claimDirectoryListings(ctx context.Context, runtimeID string) {
	requests, err := d.client.ClaimDirectoryListings(ctx, runtimeID)
	if err != nil {
		if ctx.Err() == nil {
			d.logger.Warn("directory listing: claim failed", "runtime_id", runtimeID, "error", err)
		}
		return
	}
	for _, req := range requests {
		go d.answerDirectoryListing(ctx, runtimeID, req)
	}
}

func (d *Daemon) answerDirectoryListing(ctx context.Context, runtimeID string, req DirectoryListingRequest) {
	report := directoryListingReport{}
	// Reading a directory cannot be cancelled, and one on a dead network mount
	// can hang, so the answer is given up on rather than waited for.
	found := make(chan DirectoryListing, 1)
	go func() { found <- listDirectory(req.Path, req.ProbeWrite) }()
	timer := time.NewTimer(directoryListingTimeout)
	defer timer.Stop()
	select {
	case listing := <-found:
		report.Listing = &listing
	case <-timer.C:
		report.Error = "reading the directory took too long"
	case <-ctx.Done():
		return
	}
	reportCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := d.client.ReportDirectoryListing(reportCtx, runtimeID, req.ID, report); err != nil && ctx.Err() == nil {
		d.logger.Warn("directory listing: report failed", "request_id", req.ID, "error", err)
	}
}

// listDirectory reads the directories inside path and judges path against the
// rules a local directory must meet when a task starts in it (see
// validateLocalPath). An empty path means the daemon's home directory, which
// gives the page somewhere to start from.
//
// A protected path, such as the home directory itself, is still listed: it
// cannot be chosen, but it has to be walked through to reach one that can.
func listDirectory(path string, probeWrite bool) DirectoryListing {
	out := DirectoryListing{Path: path, Separator: string(filepath.Separator), Entries: []string{}}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			out.Problem, out.Detail = directoryProblemError, err.Error()
			return out
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		out.Problem = directoryProblemNotAbsolute
		return out
	}
	path = filepath.Clean(path)
	out.Path = path

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			out.Problem = directoryProblemNotFound
		} else {
			out.Problem, out.Detail = directoryProblemNotReadable, err.Error()
		}
		return out
	}
	if !info.IsDir() {
		out.Problem = directoryProblemNotDirectory
		return out
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		out.Problem, out.Detail = directoryProblemNotReadable, err.Error()
		return out
	}
	for _, entry := range entries {
		if !isDirectoryEntry(path, entry) {
			continue
		}
		if len(out.Entries) == directoryListingMaxEntries {
			out.Truncated = true
			break
		}
		out.Entries = append(out.Entries, entry.Name())
	}
	out.IsGitRepo = insideGitWorkTree(path)

	if reason, blocked := isBlacklistedLocalPath(path); blocked {
		out.Problem, out.Detail = directoryProblemProtected, reason
		return out
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if reason, blocked := isBlacklistedRealPath(filepath.Clean(real)); blocked {
			out.Problem, out.Detail = directoryProblemProtected, reason
			return out
		}
	}
	if probeWrite {
		if err := checkDirReadWrite(path); err != nil {
			out.Problem, out.Detail = directoryProblemNotWritable, err.Error()
		}
	}
	return out
}

// isDirectoryEntry reports whether an entry is a directory, following a
// symbolic link to see what it points at.
func isDirectoryEntry(dir string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	return err == nil && info.IsDir()
}

// insideGitWorkTree reports whether path is in a git working tree: it or a
// directory above it holds a .git entry (a directory in a clone, a file in a
// linked worktree or a submodule).
func insideGitWorkTree(path string) bool {
	for dir := path; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
