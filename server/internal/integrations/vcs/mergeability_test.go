package vcs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveJSON(t *testing.T, wantPath, wantHeader, wantValue, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != wantPath {
			t.Errorf("path = %q, want %q", r.URL.EscapedPath(), wantPath)
		}
		if got := r.Header.Get(wantHeader); got != wantValue {
			t.Errorf("%s = %q, want %q", wantHeader, got, wantValue)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestForgejoPullRequestMergeability(t *testing.T) {
	cases := []struct {
		name, body string
		want       MergeState
	}{
		{"merges cleanly", `{"state":"open","mergeable":true}`, MergeStateMergeable},
		// Gitea answers false for a conflict and while it is still checking.
		{"cannot be merged", `{"state":"open","mergeable":false}`, MergeStateUnsettled},
		{"a draft is never mergeable", `{"state":"open","draft":true,"mergeable":false}`, MergeStateUnknown},
		{"merged", `{"state":"closed","merged":true,"mergeable":false}`, MergeStateUnknown},
		{"closed", `{"state":"closed","mergeable":false}`, MergeStateUnknown},
		{"no answer", `{"state":"open"}`, MergeStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := serveJSON(t, "/api/v1/repos/team/app/pulls/7", "Authorization", "token secret", tc.body)
			got, err := forgejoProvider{kind: KindGitea}.PullRequestMergeability(context.Background(), base+"/", "secret", "team", "app", 7)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.State != tc.want {
				t.Errorf("state = %q, want %q", got.State, tc.want)
			}
		})
	}
}

func TestForgejoPullRequestMergeabilityCarriesTheCommitsRead(t *testing.T) {
	base := serveJSON(t, "/api/v1/repos/team/app/pulls/7", "Authorization", "token secret",
		`{"state":"open","mergeable":true,"head":{"sha":"h1"},"base":{"sha":"b1"}}`)
	got, err := forgejoProvider{kind: KindForgejo}.PullRequestMergeability(context.Background(), base, "secret", "team", "app", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != "h1" || got.BaseSHA != "b1" {
		t.Errorf("head, base = %q, %q", got.HeadSHA, got.BaseSHA)
	}
}

func TestGitLabPullRequestMergeability(t *testing.T) {
	cases := []struct {
		name, body string
		want       MergeState
	}{
		{"merges cleanly", `{"state":"opened","has_conflicts":false,"merge_status":"can_be_merged","detailed_merge_status":"mergeable"}`, MergeStateMergeable},
		{"blocked by something other than a conflict", `{"state":"opened","has_conflicts":false,"merge_status":"can_be_merged","detailed_merge_status":"not_approved"}`, MergeStateMergeable},
		{"conflicting", `{"state":"opened","has_conflicts":true,"merge_status":"cannot_be_merged","detailed_merge_status":"conflict"}`, MergeStateConflicting},
		{"an older GitLab without the detailed status", `{"state":"opened","has_conflicts":true,"merge_status":"cannot_be_merged"}`, MergeStateConflicting},
		// has_conflicts is stale until GitLab has checked.
		{"still checking", `{"state":"opened","has_conflicts":true,"merge_status":"checking","detailed_merge_status":"checking"}`, MergeStateUnknown},
		{"not checked yet", `{"state":"opened","has_conflicts":false,"merge_status":"unchecked","detailed_merge_status":"unchecked"}`, MergeStateUnknown},
		{"merged", `{"state":"merged","has_conflicts":false}`, MergeStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The project path, subgroup included, is one escaped segment.
			base := serveJSON(t, "/api/v4/projects/group%2Fsub%2Fapp/merge_requests/3", "PRIVATE-TOKEN", "secret", tc.body)
			got, err := gitlabProvider{}.PullRequestMergeability(context.Background(), base, "secret", "group/sub", "app", 3)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.State != tc.want {
				t.Errorf("state = %q, want %q", got.State, tc.want)
			}
		})
	}
}

func TestPullRequestMergeabilityErrors(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer denied.Close()
	if _, err := (forgejoProvider{kind: KindGitea}).PullRequestMergeability(context.Background(), denied.URL, "t", "o", "r", 1); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a refused token: err = %v, want ErrUnauthorized", err)
	}

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	if _, err := (gitlabProvider{}).PullRequestMergeability(context.Background(), missing.URL, "t", "o", "r", 1); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Errorf("a missing merge request: err = %v, want a plain error", err)
	}
}

func TestEveryProviderReadsMergeability(t *testing.T) {
	for kind, provider := range registry {
		if _, ok := provider.(MergeabilityReader); !ok {
			t.Errorf("provider %q cannot be asked whether a pull request merges", kind)
		}
	}
}
