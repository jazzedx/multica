package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestRuntimeDirectoryStoreAnswersAWaitingRead(t *testing.T) {
	store := newRuntimeDirectoryStore()
	req, err := store.create("rt-1", "/srv/app", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	answered := make(chan RuntimeDirectoryRequest, 1)
	go func() {
		got, _ := store.wait(context.Background(), req.ID, 5*time.Second)
		answered <- got
	}()

	claimed := store.claim("rt-1")
	if len(claimed) != 1 || claimed[0].ID != req.ID || claimed[0].Path != "/srv/app" || !claimed[0].ProbeWrite {
		t.Fatalf("claim = %+v, want the one queued request", claimed)
	}
	if again := store.claim("rt-1"); len(again) != 0 {
		t.Fatalf("a claimed request was handed out twice: %+v", again)
	}
	if !store.report("rt-1", req.ID, &RuntimeDirectoryListing{Path: "/srv/app", Entries: []string{"src"}}, "") {
		t.Fatal("report was refused")
	}

	select {
	case got := <-answered:
		if got.Status != runtimeDirectoryCompleted || got.Listing == nil || len(got.Listing.Entries) != 1 {
			t.Fatalf("waiting read got %+v, want the completed listing", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting read was not released by the report")
	}
}

func TestRuntimeDirectoryStoreWaitReturnsUnansweredRequest(t *testing.T) {
	store := newRuntimeDirectoryStore()
	req, _ := store.create("rt-1", "/srv", false)

	got, ok := store.wait(context.Background(), req.ID, 20*time.Millisecond)

	if !ok || got.Status != runtimeDirectoryPending {
		t.Fatalf("wait = %+v, %v; want the request still pending", got, ok)
	}
}

func TestRuntimeDirectoryStoreKeepsRuntimesApart(t *testing.T) {
	store := newRuntimeDirectoryStore()
	req, _ := store.create("rt-1", "/srv", false)

	if claimed := store.claim("rt-2"); len(claimed) != 0 {
		t.Fatalf("another runtime claimed the request: %+v", claimed)
	}
	if store.report("rt-2", req.ID, &RuntimeDirectoryListing{Path: "/srv"}, "") {
		t.Fatal("another runtime's report was accepted")
	}
	if got, _ := store.get(req.ID); got.Status != runtimeDirectoryPending {
		t.Fatalf("status = %q, want it untouched", got.Status)
	}
}

func TestRuntimeDirectoryStoreTimesOut(t *testing.T) {
	store := newRuntimeDirectoryStore()

	unclaimed, _ := store.create("rt-1", "/a", false)
	store.requests[unclaimed.ID].CreatedAt = time.Now().Add(-runtimeDirectoryPendingTimeout - time.Second)
	if got, _ := store.get(unclaimed.ID); got.Status != runtimeDirectoryTimeout {
		t.Errorf("unclaimed request: status = %q, want timeout", got.Status)
	}
	if claimed := store.claim("rt-1"); len(claimed) != 0 {
		t.Errorf("a timed-out request was handed out: %+v", claimed)
	}

	unanswered, _ := store.create("rt-1", "/b", false)
	store.claim("rt-1")
	store.requests[unanswered.ID].claimedAt = time.Now().Add(-runtimeDirectoryRunningTimeout - time.Second)
	if got, _ := store.get(unanswered.ID); got.Status != runtimeDirectoryTimeout {
		t.Errorf("unanswered request: status = %q, want timeout", got.Status)
	}
	// A late answer is dropped rather than reviving the request.
	store.report("rt-1", unanswered.ID, &RuntimeDirectoryListing{Path: "/b"}, "")
	if got, _ := store.get(unanswered.ID); got.Status != runtimeDirectoryTimeout || got.Listing != nil {
		t.Errorf("late answer changed the request: %+v", got)
	}
}

func TestRuntimeDirectoryStoreLimitsOpenRequests(t *testing.T) {
	store := newRuntimeDirectoryStore()
	for i := 0; i < runtimeDirectoryMaxOpen; i++ {
		if _, err := store.create("rt-1", "/srv", false); err != nil {
			t.Fatalf("request %d refused: %v", i, err)
		}
	}
	if _, err := store.create("rt-1", "/srv", false); err == nil {
		t.Fatal("a request past the limit was accepted")
	}
	if _, err := store.create("rt-2", "/srv", false); err != nil {
		t.Fatalf("another runtime was held to this one's limit: %v", err)
	}
}

func TestBoundRuntimeDirectoryListing(t *testing.T) {
	entries := make([]string, 0, maxRuntimeDirectoryEntries+10)
	for i := 0; i < maxRuntimeDirectoryEntries+10; i++ {
		entries = append(entries, "dir")
	}
	entries[3] = ""
	got := boundRuntimeDirectoryListing(&RuntimeDirectoryListing{Path: "/srv", Separator: "?", Entries: entries})

	if len(got.Entries) != maxRuntimeDirectoryEntries || !got.Truncated {
		t.Errorf("entries = %d, truncated = %v", len(got.Entries), got.Truncated)
	}
	if got.Separator != "/" {
		t.Errorf("separator = %q, want an unknown one read as /", got.Separator)
	}
	if boundRuntimeDirectoryListing(nil) != nil {
		t.Error("a missing listing must stay missing")
	}
}

func directoryRuntime(t *testing.T, over ...testutil.Cols) string {
	t.Helper()
	return dbfx.Runtime(t, fmt.Sprintf("directory runtime %d", time.Now().UnixNano()), over...)
}

func askForDirectory(userID, runtimeID, path string) *http.Request {
	return withURLParam(newRequestAs(userID, http.MethodPost, "/api/runtimes/"+runtimeID+"/directories",
		map[string]any{"path": path}), "runtimeId", runtimeID)
}

// The page's request stays open until the daemon has claimed the listing and
// reported what it read, and returns that answer.
func TestRuntimeDirectoryListingRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := directoryRuntime(t)

	asked := make(chan RuntimeDirectoryRequest, 1)
	go func() {
		var out RuntimeDirectoryRequest
		testutil.Call(t, testHandler.InitiateListRuntimeDirectory, askForDirectory(testUserID, runtimeID, "/srv/app")).
			Want(http.StatusOK).JSON(&out)
		asked <- out
	}()

	var claimed claimRuntimeDirectoryResponse
	deadline := time.Now().Add(2 * time.Second)
	for len(claimed.Requests) == 0 && time.Now().Before(deadline) {
		req := withURLParam(newOwnerDaemonRequest(http.MethodPost, "/", nil), "runtimeId", runtimeID)
		testutil.Call(t, testHandler.ClaimRuntimeDirectoryRequests, req).Want(http.StatusOK).JSON(&claimed)
		if len(claimed.Requests) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(claimed.Requests) != 1 || claimed.Requests[0].Path != "/srv/app" {
		t.Fatalf("claimed = %+v, want the listing that was asked for", claimed.Requests)
	}

	report := withURLParams(newOwnerDaemonRequest(http.MethodPost, "/", map[string]any{
		"listing": map[string]any{"path": "/srv/app", "separator": "/", "entries": []string{"api", "web"}, "is_git_repo": true},
	}), "runtimeId", runtimeID, "requestId", claimed.Requests[0].ID)
	testutil.Call(t, testHandler.ReportRuntimeDirectoryResult, report).Want(http.StatusOK)

	select {
	case got := <-asked:
		if got.Status != runtimeDirectoryCompleted || got.Listing == nil {
			t.Fatalf("answer = %+v, want a completed listing", got)
		}
		if len(got.Listing.Entries) != 2 || !got.Listing.IsGitRepo {
			t.Errorf("listing = %+v", got.Listing)
		}
	case <-time.After(runtimeDirectoryWait):
		t.Fatal("the request was not answered by the report")
	}
}

func TestRuntimeDirectoryListingAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("a member who may not use a private machine", func(t *testing.T) {
		runtimeID := directoryRuntime(t)
		member := plainMember(t, "directory-listing-member")
		testutil.Call(t, testHandler.InitiateListRuntimeDirectory, askForDirectory(member, runtimeID, "/srv")).
			Want(http.StatusNotFound)
	})

	t.Run("an offline machine", func(t *testing.T) {
		runtimeID := directoryRuntime(t, testutil.Cols{"status": "offline"})
		testutil.Call(t, testHandler.InitiateListRuntimeDirectory, askForDirectory(testUserID, runtimeID, "/srv")).
			Want(http.StatusServiceUnavailable)
	})

	t.Run("another daemon", func(t *testing.T) {
		runtimeID := directoryRuntime(t, testutil.Cols{"daemon_id": "directory-listing-daemon"})
		foreign := withURLParam(newDaemonTokenRequest(http.MethodPost, "/", nil, testWorkspaceID, "attacker-daemon"), "runtimeId", runtimeID)
		testutil.Call(t, testHandler.ClaimRuntimeDirectoryRequests, foreign).Want(http.StatusNotFound)
	})

	t.Run("a request read through another runtime", func(t *testing.T) {
		req, err := runtimeDirectories.create(directoryRuntime(t), "/srv", false)
		if err != nil {
			t.Fatal(err)
		}
		other := directoryRuntime(t)
		read := withURLParams(newRequestAs(testUserID, http.MethodGet, "/", nil), "runtimeId", other, "requestId", req.ID)
		testutil.Call(t, testHandler.GetRuntimeDirectoryRequest, read).Want(http.StatusNotFound)
	})
}
