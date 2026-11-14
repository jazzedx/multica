package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
}

func TestListDirectoryNamesDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "alpha", "beta", ".hidden")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantEntries := []string{".hidden", "alpha", "beta"}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "alpha"), filepath.Join(root, "link-to-dir")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "notes.txt"), filepath.Join(root, "link-to-file")); err != nil {
			t.Fatal(err)
		}
		wantEntries = []string{".hidden", "alpha", "beta", "link-to-dir"}
	}

	got := listDirectory(root+string(filepath.Separator), false)

	if got.Problem != "" {
		t.Fatalf("problem = %q (%s), want none", got.Problem, got.Detail)
	}
	if got.Path != root {
		t.Errorf("path = %q, want the cleaned %q", got.Path, root)
	}
	if !reflect.DeepEqual(got.Entries, wantEntries) {
		t.Errorf("entries = %v, want %v", got.Entries, wantEntries)
	}
	if got.IsGitRepo {
		t.Error("a plain directory was reported as a git repository")
	}
	if got.Separator != string(filepath.Separator) {
		t.Errorf("separator = %q", got.Separator)
	}
}

func TestListDirectoryProblems(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, want string
	}{
		{"relative", "projects/app", directoryProblemNotAbsolute},
		{"missing", filepath.Join(root, "nope"), directoryProblemNotFound},
		{"file", file, directoryProblemNotDirectory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listDirectory(tc.path, false)
			if got.Problem != tc.want {
				t.Errorf("problem = %q, want %q", got.Problem, tc.want)
			}
			if got.Entries == nil {
				t.Error("entries must be an empty list, not null")
			}
		})
	}
}

func TestListDirectoryFindsGitWorkTree(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "repo/.git", "repo/src/deep", "plain")
	// A linked worktree and a submodule mark themselves with a .git file.
	mkdirs(t, root, "linked")
	if err := os.WriteFile(filepath.Join(root, "linked", ".git"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"repo":          true,
		"repo/src/deep": true,
		"linked":        true,
		"plain":         false,
	} {
		if got := listDirectory(filepath.Join(root, path), false).IsGitRepo; got != want {
			t.Errorf("%s: is_git_repo = %v, want %v", path, got, want)
		}
	}
}

func TestListDirectoryEmptyPathStartsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mkdirs(t, home, "code")

	got := listDirectory("", false)

	if got.Path != home {
		t.Fatalf("path = %q, want home %q", got.Path, home)
	}
	if !reflect.DeepEqual(got.Entries, []string{"code"}) {
		t.Errorf("entries = %v, want the directories in home", got.Entries)
	}
	// Home can be walked through but not chosen.
	if got.Problem != directoryProblemProtected {
		t.Errorf("problem = %q, want %q", got.Problem, directoryProblemProtected)
	}
}

func TestListDirectoryProbesWriteOnlyWhenAsked(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind here")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mkdirs(t, root, "locked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got := listDirectory(locked, false); got.Problem != "" {
		t.Errorf("without a probe: problem = %q, want none", got.Problem)
	}
	if got := listDirectory(locked, true); got.Problem != directoryProblemNotWritable {
		t.Errorf("with a probe: problem = %q, want %q", got.Problem, directoryProblemNotWritable)
	}
	if left, _ := os.ReadDir(locked); len(left) != 0 {
		t.Errorf("the probe left files behind: %v", left)
	}
}

func TestListDirectoryCapsEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < directoryListingMaxEntries+5; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := listDirectory(root, false)
	if len(got.Entries) != directoryListingMaxEntries || !got.Truncated {
		t.Errorf("entries = %d, truncated = %v; want %d and true", len(got.Entries), got.Truncated, directoryListingMaxEntries)
	}
}

// A directory-listing hint makes the daemon claim what is queued and report
// what it read, without a heartbeat in between.
func TestDirectoryListingHintClaimsAndReports(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "one", "two")
	reported := make(chan directoryListingReport, 1)
	d, _ := pendingWorkHintDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/runtimes/rt-1/directories/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"requests": []DirectoryListingRequest{{ID: "req-1", Path: root}},
			})
		case "/api/daemon/runtimes/rt-1/directories/req-1/result":
			var report directoryListingReport
			_ = json.NewDecoder(r.Body).Decode(&report)
			reported <- report
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	d.handlePendingWorkHint("rt-1", protocol.PendingWorkKindDirectoryListing)

	select {
	case report := <-reported:
		if report.Listing == nil {
			t.Fatalf("no listing reported: %q", report.Error)
		}
		if !reflect.DeepEqual(report.Listing.Entries, []string{"one", "two"}) {
			t.Errorf("entries = %v", report.Listing.Entries)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never reported the listing")
	}
}
