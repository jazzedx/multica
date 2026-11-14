package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// runtimeDirectoryBackend is one RuntimeDirectoryStore under test, with a way
// to age a stored request without waiting out its timeout.
type runtimeDirectoryBackend struct {
	store RuntimeDirectoryStore
	// rewind rewrites a stored request in place.
	rewind func(t *testing.T, id string, change func(*RuntimeDirectoryRequest))
}

// eachRuntimeDirectoryStore runs a test against the in-memory store and, when
// REDIS_TEST_URL is set, against the Redis one: both must behave the same.
func eachRuntimeDirectoryStore(t *testing.T, test func(t *testing.T, b runtimeDirectoryBackend)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		store := NewInMemoryRuntimeDirectoryStore()
		test(t, runtimeDirectoryBackend{store: store, rewind: func(_ *testing.T, id string, change func(*RuntimeDirectoryRequest)) {
			store.mu.Lock()
			defer store.mu.Unlock()
			change(store.requests[id])
		}})
	})
	t.Run("redis", func(t *testing.T) {
		store := NewRedisRuntimeDirectoryStore(newRedisTestClient(t))
		test(t, runtimeDirectoryBackend{store: store, rewind: func(t *testing.T, id string, change func(*RuntimeDirectoryRequest)) {
			t.Helper()
			ctx := context.Background()
			req, err := store.Get(ctx, id)
			if err != nil || req == nil {
				t.Fatalf("rewind: get = %+v, %v", req, err)
			}
			change(req)
			if err := store.persistRequest(ctx, req); err != nil {
				t.Fatalf("rewind: persist: %v", err)
			}
		}})
	})
}

func mustCreateDirectoryRequest(t *testing.T, store RuntimeDirectoryStore, runtimeID, path string, probeWrite bool) *RuntimeDirectoryRequest {
	t.Helper()
	req, err := store.Create(context.Background(), runtimeID, path, probeWrite)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return req
}

func mustClaimDirectoryRequests(t *testing.T, store RuntimeDirectoryStore, runtimeID string) []RuntimeDirectoryRequest {
	t.Helper()
	claimed, err := store.Claim(context.Background(), runtimeID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return claimed
}

func mustGetDirectoryRequest(t *testing.T, store RuntimeDirectoryStore, id string) *RuntimeDirectoryRequest {
	t.Helper()
	req, err := store.Get(context.Background(), id)
	if err != nil || req == nil {
		t.Fatalf("get = %+v, %v", req, err)
	}
	return req
}

func TestRuntimeDirectoryStoreAnswersAWaitingRead(t *testing.T) {
	eachRuntimeDirectoryStore(t, func(t *testing.T, b runtimeDirectoryBackend) {
		ctx := context.Background()
		req := mustCreateDirectoryRequest(t, b.store, "rt-1", "/srv/app", true)

		answered := make(chan *RuntimeDirectoryRequest, 1)
		go func() {
			got, _ := b.store.Wait(ctx, req.ID, 5*time.Second)
			answered <- got
		}()

		claimed := mustClaimDirectoryRequests(t, b.store, "rt-1")
		if len(claimed) != 1 || claimed[0].ID != req.ID || claimed[0].Path != "/srv/app" || !claimed[0].ProbeWrite {
			t.Fatalf("claim = %+v, want the one queued request", claimed)
		}
		if again := mustClaimDirectoryRequests(t, b.store, "rt-1"); len(again) != 0 {
			t.Fatalf("a claimed request was handed out twice: %+v", again)
		}
		known, err := b.store.Report(ctx, "rt-1", req.ID, &RuntimeDirectoryListing{Path: "/srv/app", Entries: []string{"src"}}, "")
		if err != nil || !known {
			t.Fatalf("report = %v, %v; want it accepted", known, err)
		}

		select {
		case got := <-answered:
			if got == nil || got.Status != runtimeDirectoryCompleted || got.Listing == nil || len(got.Listing.Entries) != 1 {
				t.Fatalf("waiting read got %+v, want the completed listing", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the waiting read was not released by the report")
		}
	})
}

func TestRuntimeDirectoryStoreWaitReturnsUnansweredRequest(t *testing.T) {
	eachRuntimeDirectoryStore(t, func(t *testing.T, b runtimeDirectoryBackend) {
		req := mustCreateDirectoryRequest(t, b.store, "rt-1", "/srv", false)

		got, err := b.store.Wait(context.Background(), req.ID, 20*time.Millisecond)

		if err != nil || got == nil || got.Status != runtimeDirectoryPending {
			t.Fatalf("wait = %+v, %v; want the request still pending", got, err)
		}
		if unknown, err := b.store.Wait(context.Background(), "no-such-request", 20*time.Millisecond); err != nil || unknown != nil {
			t.Fatalf("wait on an unknown request = %+v, %v; want nothing", unknown, err)
		}
	})
}

func TestRuntimeDirectoryStoreKeepsRuntimesApart(t *testing.T) {
	eachRuntimeDirectoryStore(t, func(t *testing.T, b runtimeDirectoryBackend) {
		req := mustCreateDirectoryRequest(t, b.store, "rt-1", "/srv", false)

		if claimed := mustClaimDirectoryRequests(t, b.store, "rt-2"); len(claimed) != 0 {
			t.Fatalf("another runtime claimed the request: %+v", claimed)
		}
		if known, _ := b.store.Report(context.Background(), "rt-2", req.ID, &RuntimeDirectoryListing{Path: "/srv"}, ""); known {
			t.Fatal("another runtime's report was accepted")
		}
		if got := mustGetDirectoryRequest(t, b.store, req.ID); got.Status != runtimeDirectoryPending {
			t.Fatalf("status = %q, want it untouched", got.Status)
		}
	})
}

func TestRuntimeDirectoryStoreTimesOut(t *testing.T) {
	eachRuntimeDirectoryStore(t, func(t *testing.T, b runtimeDirectoryBackend) {
		unclaimed := mustCreateDirectoryRequest(t, b.store, "rt-1", "/a", false)
		b.rewind(t, unclaimed.ID, func(req *RuntimeDirectoryRequest) {
			req.CreatedAt = time.Now().Add(-runtimeDirectoryPendingTimeout - time.Second)
		})
		if got := mustGetDirectoryRequest(t, b.store, unclaimed.ID); got.Status != runtimeDirectoryTimeout {
			t.Errorf("unclaimed request: status = %q, want timeout", got.Status)
		}
		if claimed := mustClaimDirectoryRequests(t, b.store, "rt-1"); len(claimed) != 0 {
			t.Errorf("a timed-out request was handed out: %+v", claimed)
		}

		unanswered := mustCreateDirectoryRequest(t, b.store, "rt-1", "/b", false)
		mustClaimDirectoryRequests(t, b.store, "rt-1")
		b.rewind(t, unanswered.ID, func(req *RuntimeDirectoryRequest) {
			aged := time.Now().Add(-runtimeDirectoryRunningTimeout - time.Second)
			req.ClaimedAt = &aged
		})
		if got := mustGetDirectoryRequest(t, b.store, unanswered.ID); got.Status != runtimeDirectoryTimeout {
			t.Errorf("unanswered request: status = %q, want timeout", got.Status)
		}
		// A late answer is dropped rather than reviving the request.
		if _, err := b.store.Report(context.Background(), "rt-1", unanswered.ID, &RuntimeDirectoryListing{Path: "/b"}, ""); err != nil {
			t.Fatalf("late report: %v", err)
		}
		if got := mustGetDirectoryRequest(t, b.store, unanswered.ID); got.Status != runtimeDirectoryTimeout || got.Listing != nil {
			t.Errorf("late answer changed the request: %+v", got)
		}
	})
}

func TestRuntimeDirectoryStoreLimitsOpenRequests(t *testing.T) {
	eachRuntimeDirectoryStore(t, func(t *testing.T, b runtimeDirectoryBackend) {
		ctx := context.Background()
		var first *RuntimeDirectoryRequest
		for i := 0; i < runtimeDirectoryMaxOpen; i++ {
			req, err := b.store.Create(ctx, "rt-1", "/srv", false)
			if err != nil {
				t.Fatalf("request %d refused: %v", i, err)
			}
			if first == nil {
				first = req
			}
		}
		if _, err := b.store.Create(ctx, "rt-1", "/srv", false); !errors.Is(err, errRuntimeDirectoryBusy) {
			t.Fatalf("a request past the limit: err = %v, want busy", err)
		}
		if _, err := b.store.Create(ctx, "rt-2", "/srv", false); err != nil {
			t.Fatalf("another runtime was held to this one's limit: %v", err)
		}
		// A request that has timed out no longer counts against the limit.
		b.rewind(t, first.ID, func(req *RuntimeDirectoryRequest) {
			req.CreatedAt = time.Now().Add(-runtimeDirectoryPendingTimeout - time.Second)
		})
		if _, err := b.store.Create(ctx, "rt-1", "/srv", false); err != nil {
			t.Fatalf("a timed-out request still held a slot: %v", err)
		}
	})
}

// A claimed request records when it was taken on every node, or none could
// time it out.
func TestRedisRuntimeDirectoryStoreEnvelopeKeepsClaimedAt(t *testing.T) {
	store := &RedisRuntimeDirectoryStore{}
	claimed := time.Now().UTC().Truncate(time.Microsecond)
	data, err := store.marshalRequest(&RuntimeDirectoryRequest{ID: "id-1", RuntimeID: "rt-1", Status: runtimeDirectoryRunning, ClaimedAt: &claimed})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := store.unmarshalRequest(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ClaimedAt == nil || !got.ClaimedAt.Equal(claimed) || got.ID != "id-1" || got.Status != runtimeDirectoryRunning {
		t.Fatalf("round trip = %+v, want ClaimedAt %s kept", got, claimed)
	}
}

// Two API nodes share one Redis: a request queued through one is claimed
// through the other exactly once, and the answer is read back through the
// first.
func TestRedisRuntimeDirectoryStoreAcrossNodes(t *testing.T) {
	rdb := newRedisTestClient(t)
	ctx := context.Background()
	nodeA, nodeB := NewRedisRuntimeDirectoryStore(rdb), NewRedisRuntimeDirectoryStore(rdb)

	req := mustCreateDirectoryRequest(t, nodeA, "rt-1", "/srv/app", false)
	claimedB := mustClaimDirectoryRequests(t, nodeB, "rt-1")
	claimedA := mustClaimDirectoryRequests(t, nodeA, "rt-1")
	if len(claimedB) != 1 || len(claimedA) != 0 {
		t.Fatalf("claims = %d on one node and %d on the other, want 1 and 0", len(claimedB), len(claimedA))
	}
	if known, err := nodeB.Report(ctx, "rt-1", req.ID, &RuntimeDirectoryListing{Path: "/srv/app"}, ""); err != nil || !known {
		t.Fatalf("report = %v, %v", known, err)
	}
	got, err := nodeA.Wait(ctx, req.ID, time.Second)
	if err != nil || got == nil || got.Status != runtimeDirectoryCompleted {
		t.Fatalf("read on the first node = %+v, %v; want the completed listing", got, err)
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
		req, err := testHandler.RuntimeDirectoryStore.Create(context.Background(), directoryRuntime(t), "/srv", false)
		if err != nil {
			t.Fatal(err)
		}
		other := directoryRuntime(t)
		read := withURLParams(newRequestAs(testUserID, http.MethodGet, "/", nil), "runtimeId", other, "requestId", req.ID)
		testutil.Call(t, testHandler.GetRuntimeDirectoryRequest, read).Want(http.StatusNotFound)
	})
}
