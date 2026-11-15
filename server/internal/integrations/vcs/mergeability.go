package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Whether a pull request merges cleanly into its base. The webhooks do not say
// so reliably, and a pull request starts conflicting when its base moves, with
// no event of its own, so this is read from the provider's API.

// MergeState answers only "is there a conflict".
type MergeState string

const (
	MergeStateMergeable   MergeState = "mergeable"
	MergeStateConflicting MergeState = "conflicting"
	// MergeStateUnknown: the provider has not decided, or the pull request is
	// in a state where the question does not apply (closed, merged, a draft on
	// a provider that does not check drafts).
	MergeStateUnknown MergeState = "unknown"
	// MergeStateUnsettled: the provider says "cannot be merged" without saying
	// why. Forgejo and Gitea answer that both for a conflict and while they are
	// still checking, so one reading does not tell the two apart; the same
	// answer for the same head and base a little later does. Never stored.
	MergeStateUnsettled MergeState = "unsettled"
)

// Mergeability is one reading of a pull request.
type Mergeability struct {
	State MergeState
	// HeadSHA and BaseSHA are the commits the reading is for.
	HeadSHA string
	BaseSHA string
}

// MergeabilityReader is implemented by the providers that can be asked.
type MergeabilityReader interface {
	PullRequestMergeability(ctx context.Context, instanceURL, token, owner, repo string, number int32) (Mergeability, error)
}

func getProviderJSON(ctx context.Context, endpoint string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header = header
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func (p forgejoProvider) PullRequestMergeability(ctx context.Context, instanceURL, token, owner, repo string, number int32) (Mergeability, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d",
		NormalizeInstanceURL(instanceURL), url.PathEscape(owner), url.PathEscape(repo), number)
	var pr struct {
		State     string `json:"state"`
		Merged    bool   `json:"merged"`
		Draft     bool   `json:"draft"`
		Mergeable *bool  `json:"mergeable"`
		Head      struct {
			Sha string `json:"sha"`
		} `json:"head"`
		Base struct {
			Sha string `json:"sha"`
		} `json:"base"`
	}
	if err := getProviderJSON(ctx, endpoint, http.Header{"Authorization": {"token " + token}}, &pr); err != nil {
		return Mergeability{}, fmt.Errorf("%s: GET pull request: %w", p.kind, err)
	}
	out := Mergeability{State: MergeStateUnknown, HeadSHA: pr.Head.Sha, BaseSHA: pr.Base.Sha}
	switch {
	case pr.State != "open" || pr.Merged:
	// A work-in-progress pull request is reported as not mergeable whether or
	// not it conflicts.
	case pr.Draft || pr.Mergeable == nil:
	case *pr.Mergeable:
		out.State = MergeStateMergeable
	default:
		out.State = MergeStateUnsettled
	}
	return out, nil
}

func (gitlabProvider) PullRequestMergeability(ctx context.Context, instanceURL, token, owner, repo string, number int32) (Mergeability, error) {
	// A project is addressed by its URL-encoded full path, subgroups included.
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d",
		NormalizeInstanceURL(instanceURL), url.PathEscape(owner+"/"+repo), number)
	var mr struct {
		State               string `json:"state"`
		HasConflicts        bool   `json:"has_conflicts"`
		MergeStatus         string `json:"merge_status"`
		DetailedMergeStatus string `json:"detailed_merge_status"`
		Sha                 string `json:"sha"`
		DiffRefs            struct {
			BaseSha string `json:"base_sha"`
		} `json:"diff_refs"`
	}
	if err := getProviderJSON(ctx, endpoint, http.Header{"PRIVATE-TOKEN": {token}}, &mr); err != nil {
		return Mergeability{}, fmt.Errorf("gitlab: GET merge request: %w", err)
	}
	out := Mergeability{State: MergeStateUnknown, HeadSHA: mr.Sha, BaseSHA: mr.DiffRefs.BaseSha}
	if mr.State != "opened" {
		return out, nil
	}
	// GitLab computes mergeability lazily; has_conflicts is stale until it has.
	switch mr.DetailedMergeStatus {
	case "unchecked", "checking", "preparing", "approvals_syncing":
		return out, nil
	}
	switch mr.MergeStatus {
	case "unchecked", "checking", "cannot_be_merged_recheck", "cannot_be_merged_rechecking":
		return out, nil
	}
	if mr.HasConflicts || mr.DetailedMergeStatus == "conflict" {
		out.State = MergeStateConflicting
	} else {
		out.State = MergeStateMergeable
	}
	return out, nil
}
