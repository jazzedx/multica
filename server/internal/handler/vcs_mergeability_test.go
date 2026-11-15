package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestSettleVCSMergeability(t *testing.T) {
	reading := func(state vcs.MergeState, head, base string) vcs.Mergeability {
		return vcs.Mergeability{State: state, HeadSHA: head, BaseSHA: base}
	}
	cases := []struct {
		name      string
		readings  []vcs.Mergeability
		want      vcs.MergeState
		wantReads int
	}{
		{"a clean merge needs one reading", []vcs.Mergeability{reading(vcs.MergeStateMergeable, "h", "b")}, vcs.MergeStateMergeable, 1},
		{"a provider that names the conflict needs one reading", []vcs.Mergeability{reading(vcs.MergeStateConflicting, "h", "b")}, vcs.MergeStateConflicting, 1},
		{"cannot be merged twice for the same commits is a conflict",
			[]vcs.Mergeability{reading(vcs.MergeStateUnsettled, "h", "b"), reading(vcs.MergeStateUnsettled, "h", "b")}, vcs.MergeStateConflicting, 2},
		{"a check that was still running clears",
			[]vcs.Mergeability{reading(vcs.MergeStateUnsettled, "h", "b"), reading(vcs.MergeStateMergeable, "h", "b")}, vcs.MergeStateMergeable, 2},
		{"a base that moved in between leaves it open",
			[]vcs.Mergeability{reading(vcs.MergeStateUnsettled, "h", "b1"), reading(vcs.MergeStateUnsettled, "h", "b2")}, vcs.MergeStateUnknown, 2},
		{"a head that moved in between leaves it open",
			[]vcs.Mergeability{reading(vcs.MergeStateUnsettled, "h1", "b"), reading(vcs.MergeStateUnsettled, "h2", "b")}, vcs.MergeStateUnknown, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads, waits := 0, 0
			got, err := settleVCSMergeability(
				func() (vcs.Mergeability, error) { reads++; return tc.readings[reads-1], nil },
				func() { waits++ },
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want || reads != tc.wantReads || waits != tc.wantReads-1 {
				t.Errorf("state = %q after %d reads and %d waits; want %q after %d reads", got.State, reads, waits, tc.want, tc.wantReads)
			}
		})
	}

	t.Run("a failed read is an error, not a verdict", func(t *testing.T) {
		boom := errors.New("boom")
		if _, err := settleVCSMergeability(func() (vcs.Mergeability, error) { return vcs.Mergeability{}, boom }, func() {}); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the read's error", err)
		}
	})
}

// fakeMergeabilityProvider stands in for the provider and removes the waits.
// Readings are keyed by pull request number.
type fakeMergeabilityProvider struct {
	mu       sync.Mutex
	readings map[int32]vcs.Mergeability
	reads    map[int32]int
}

func withFakeMergeabilityProvider(t *testing.T) *fakeMergeabilityProvider {
	t.Helper()
	fake := &fakeMergeabilityProvider{readings: map[int32]vcs.Mergeability{}, reads: map[int32]int{}}
	prevRead, prevSettle, prevConfirm := vcsReadMergeability, vcsMergeabilitySettleDelay, vcsMergeabilityConfirmDelay
	vcsMergeabilitySettleDelay, vcsMergeabilityConfirmDelay = 0, 0
	vcsReadMergeability = func(_ context.Context, _ db.VcsConnection, token string, pr db.VcsPullRequest) (vcs.Mergeability, error) {
		if token != "tok" {
			return vcs.Mergeability{}, fmt.Errorf("read with token %q, want the connection's", token)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.reads[pr.PrNumber]++
		got, ok := fake.readings[pr.PrNumber]
		if !ok {
			return vcs.Mergeability{}, errors.New("no reading")
		}
		return got, nil
	}
	t.Cleanup(func() {
		vcsReadMergeability, vcsMergeabilitySettleDelay, vcsMergeabilityConfirmDelay = prevRead, prevSettle, prevConfirm
	})
	return fake
}

func (f *fakeMergeabilityProvider) set(number int32, state vcs.MergeState, head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readings[number] = vcs.Mergeability{State: state, HeadSHA: head, BaseSHA: "base"}
}

func (f *fakeMergeabilityProvider) readCount(number int32) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[number]
}

type vcsMergeabilityFixture struct {
	conn  db.VcsConnection
	issue IssueResponse
}

func newVCSMergeabilityFixture(t *testing.T) vcsMergeabilityFixture {
	t.Helper()
	ctx := context.Background()
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "gitea", fmt.Sprintf("https://gitea-%d.test", time.Now().UnixNano()))
	issue := newVCSIssue(t, "Mergeability")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM vcs_pull_request_mergeability m USING vcs_pull_request p WHERE p.id = m.pull_request_id AND p.connection_id = $1`, connID)
		cleanupVCS(ctx, issue.ID)
	})
	conn, err := testHandler.Queries.GetVCSConnectionByID(ctx, parseUUID(connID))
	if err != nil {
		t.Fatal(err)
	}
	return vcsMergeabilityFixture{conn: conn, issue: issue}
}

// pullRequest mirrors a pull request of the fixture's repository; linked ones
// name the fixture's issue in the title.
func (f vcsMergeabilityFixture) pullRequest(t *testing.T, number int32, state, head string, linked bool) db.VcsPullRequest {
	t.Helper()
	ctx := context.Background()
	pr, err := testHandler.Queries.UpsertVCSPullRequest(ctx, db.UpsertVCSPullRequestParams{
		WorkspaceID:  f.conn.WorkspaceID,
		ConnectionID: f.conn.ID,
		Provider:     f.conn.Provider,
		RepoOwner:    "acme",
		RepoName:     "widget",
		PrNumber:     number,
		Title:        "PR",
		State:        state,
		HtmlUrl:      fmt.Sprintf("https://gitea.test/acme/widget/pulls/%d", number),
		PrCreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PrUpdatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		HeadSha:      head,
	})
	if err != nil {
		t.Fatalf("upsert pull request: %v", err)
	}
	if linked {
		if _, err := testHandler.Queries.LinkIssueToVCSPullRequest(ctx, db.LinkIssueToVCSPullRequestParams{IssueID: parseUUID(f.issue.ID), PullRequestID: pr.ID}); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	return pr
}

func storedVCSMergeability(t *testing.T, pr db.VcsPullRequest) (state, head string) {
	t.Helper()
	row, err := testHandler.Queries.GetVCSPullRequestMergeability(context.Background(), pr.ID)
	if err != nil {
		return "", ""
	}
	return row.State, row.HeadSha
}

func waitForVCSMergeability(t *testing.T, pr db.VcsPullRequest, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := storedVCSMergeability(t, pr); state == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := storedVCSMergeability(t, pr)
	t.Fatalf("pull request #%d: stored verdict = %q, want %q", pr.PrNumber, state, want)
}

func TestRefreshVCSMergeabilityStoresTheVerdict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	pr := f.pullRequest(t, 11, "open", "h1", true)

	// Gitea's bare "cannot be merged" is confirmed by a second reading.
	fake.set(11, vcs.MergeStateUnsettled, "h1")
	testHandler.refreshVCSMergeability(f.conn, pr, 0)
	if state, head := storedVCSMergeability(t, pr); state != "conflicting" || head != "h1" {
		t.Fatalf("stored = %q for %q, want conflicting for h1", state, head)
	}
	if n := fake.readCount(11); n != 2 {
		t.Errorf("provider read %d times, want 2", n)
	}

	fake.set(11, vcs.MergeStateMergeable, "h2")
	testHandler.refreshVCSMergeability(f.conn, pr, 0)
	if state, head := storedVCSMergeability(t, pr); state != "mergeable" || head != "h2" {
		t.Fatalf("stored = %q for %q, want mergeable for the head that was read", state, head)
	}
}

func TestRefreshVCSMergeabilitySkipsUnlinkedPullRequests(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	pr := f.pullRequest(t, 12, "open", "h1", false)
	fake.set(12, vcs.MergeStateMergeable, "h1")

	testHandler.refreshVCSMergeability(f.conn, pr, 0)

	if n := fake.readCount(12); n != 0 {
		t.Errorf("a pull request no issue is linked to cost %d requests", n)
	}
}

func TestRefreshVCSMergeabilityKeepsTheVerdictWhenTheProviderFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	pr := f.pullRequest(t, 13, "open", "h1", true)
	fake.set(13, vcs.MergeStateMergeable, "h1")
	testHandler.refreshVCSMergeability(f.conn, pr, 0)

	fake.mu.Lock()
	delete(fake.readings, 13)
	fake.mu.Unlock()
	testHandler.refreshVCSMergeability(f.conn, pr, 0)

	if state, _ := storedVCSMergeability(t, pr); state != "mergeable" {
		t.Errorf("a failed read replaced the verdict with %q", state)
	}
}

// A merge moves the base of every other open pull request in the repository,
// none of which gets an event of its own.
func TestVCSMergeRereadsTheOtherOpenPullRequests(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	merged := f.pullRequest(t, 20, "merged", "m", true)
	open := f.pullRequest(t, 21, "open", "o", true)
	draft := f.pullRequest(t, 22, "draft", "d", true)
	closed := f.pullRequest(t, 23, "closed", "c", true)
	unlinked := f.pullRequest(t, 24, "open", "u", false)
	fake.set(21, vcs.MergeStateConflicting, "o")
	fake.set(22, vcs.MergeStateUnknown, "d")
	fake.set(23, vcs.MergeStateMergeable, "c")
	fake.set(24, vcs.MergeStateMergeable, "u")

	testHandler.afterVCSPullRequestMirrored(f.conn, merged, "open")

	waitForVCSMergeability(t, open, "conflicting")
	waitForVCSMergeability(t, draft, "unknown")
	if n := fake.readCount(23) + fake.readCount(24) + fake.readCount(20); n != 0 {
		t.Errorf("closed, unlinked and the merged pull request itself cost %d requests", n)
	}
	if state, _ := storedVCSMergeability(t, closed); state != "" {
		t.Errorf("a closed pull request got a verdict: %q", state)
	}
	_ = unlinked

	// A later event of the already merged pull request reads nothing again.
	before := fake.readCount(21)
	testHandler.afterVCSPullRequestMirrored(f.conn, merged, "merged")
	time.Sleep(50 * time.Millisecond)
	if fake.readCount(21) != before {
		t.Error("a repeated event of a merged pull request re-read the others")
	}
}

func TestSweepVCSMergeabilityRereadsWatchedPullRequests(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	watched := f.pullRequest(t, 30, "open", "w", true)
	fake.set(30, vcs.MergeStateConflicting, "w")

	// Not watched by any rule: the sweep leaves it alone.
	testHandler.SweepVCSMergeability(ctx)
	time.Sleep(50 * time.Millisecond)
	if n := fake.readCount(30); n != 0 {
		t.Fatalf("an unwatched pull request was read %d times", n)
	}

	dbfx.Insert(t, "issue_wakeup", testutil.Cols{
		"id":           testutil.Raw("gen_random_uuid()"),
		"workspace_id": testWorkspaceID, "issue_id": f.issue.ID,
		"kind": "event", "mode": "continuous", "instruction": "Resolve the conflict", "enabled": true,
		"condition": testutil.Raw(`'{"type":"pull_request","event":"conflicts"}'::jsonb`),
	})

	testHandler.SweepVCSMergeability(ctx)
	waitForVCSMergeability(t, watched, "conflicting")

	// A fresh verdict is not read again; an aged one is.
	reads := fake.readCount(30)
	testHandler.SweepVCSMergeability(ctx)
	time.Sleep(50 * time.Millisecond)
	if fake.readCount(30) != reads {
		t.Error("a fresh verdict was read again")
	}
	if _, err := testPool.Exec(ctx, `UPDATE vcs_pull_request_mergeability SET checked_at = now() - interval '1 hour' WHERE pull_request_id = $1`, watched.ID); err != nil {
		t.Fatal(err)
	}
	fake.set(30, vcs.MergeStateMergeable, "w")
	testHandler.SweepVCSMergeability(ctx)
	waitForVCSMergeability(t, watched, "mergeable")

	// A verdict whose pull request is gone is dropped.
	orphan := "00000000-0000-4000-8000-0000000000aa"
	if _, err := testPool.Exec(ctx, `INSERT INTO vcs_pull_request_mergeability(pull_request_id,head_sha,state) VALUES($1,'x','mergeable') ON CONFLICT DO NOTHING`, orphan); err != nil {
		t.Fatal(err)
	}
	testHandler.SweepVCSMergeability(ctx)
	var left int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM vcs_pull_request_mergeability WHERE pull_request_id = $1`, orphan).Scan(&left); err != nil || left != 0 {
		t.Errorf("orphan verdict rows left = %d (err %v)", left, err)
	}
}

// The pull request card shows the stored verdict only for the head it shows.
func TestListIssuePullRequestsCarriesVCSMergeability(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := withFakeMergeabilityProvider(t)
	f := newVCSMergeabilityFixture(t)
	pr := f.pullRequest(t, 40, "open", "h1", true)
	fake.set(40, vcs.MergeStateConflicting, "h1")
	testHandler.refreshVCSMergeability(f.conn, pr, 0)

	list := func() *string {
		t.Helper()
		var out struct {
			PullRequests []GitHubPullRequestResponse `json:"pull_requests"`
		}
		req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+f.issue.ID+"/pull-requests", nil), "id", f.issue.ID)
		w := httptest.NewRecorder()
		testHandler.ListPullRequestsForIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.PullRequests) != 1 {
			t.Fatalf("pull requests = %d, want 1", len(out.PullRequests))
		}
		return out.PullRequests[0].Mergeable
	}

	if got := list(); got == nil || *got != "conflicting" {
		t.Fatalf("mergeable = %v, want conflicting", got)
	}
	// A pushed head makes the verdict stale until it is read again.
	f.pullRequest(t, 40, "open", "h2", false)
	if got := list(); got != nil {
		t.Errorf("mergeable = %q for a head that has not been read", *got)
	}
}
