package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Directory listings let a member choose a folder on a runtime's machine from
// the web: the page asks which directories exist under a path, and whether the
// path can be used as a project's local directory. The server cannot reach the
// daemon, so the question is queued, the daemon is told to come and claim it,
// and the answer is reported back.
//
// The question is asked while someone is typing, so the calls that read a
// request wait briefly for its answer instead of leaving the page to poll.
// That needs the waiter and the report to meet in one process, which is why
// requests are kept in memory here: this works on one API node only.

const (
	runtimeDirectoryPending   = "pending"
	runtimeDirectoryRunning   = "running"
	runtimeDirectoryCompleted = "completed"
	runtimeDirectoryFailed    = "failed"
	runtimeDirectoryTimeout   = "timeout"
)

const (
	// A daemon that understands the hint claims within moments. One that does
	// not never will, so the page should learn that quickly and let the path be
	// typed by hand.
	runtimeDirectoryPendingTimeout = 5 * time.Second
	runtimeDirectoryRunningTimeout = 15 * time.Second
	runtimeDirectoryRetention      = time.Minute
	// How long a read holds the connection open for an answer.
	runtimeDirectoryWait = 3 * time.Second
	// Unanswered requests one runtime may hold at a time.
	runtimeDirectoryMaxOpen = 32

	maxRuntimeDirectoryPath    = 4096
	maxRuntimeDirectoryEntries = 2000
	maxRuntimeDirectoryName    = 255
	maxRuntimeDirectoryDetail  = 500
)

var errRuntimeDirectoryBusy = errors.New("too many directory listings waiting for this runtime")

// RuntimeDirectoryListing is what the daemon found at a path.
type RuntimeDirectoryListing struct {
	// Path is the absolute path that was read. It differs from the path asked
	// for when that was empty (the daemon's home directory) or not clean.
	Path      string `json:"path"`
	Separator string `json:"separator"`
	// Entries are the names of the directories inside Path. Files are left out.
	Entries   []string `json:"entries"`
	Truncated bool     `json:"truncated"`
	IsGitRepo bool     `json:"is_git_repo"`
	// Problem is why Path cannot be a project's local directory: not_absolute,
	// not_found, not_a_directory, not_readable, not_writable, protected or
	// error. Empty when nothing was found wrong. A protected path can still be
	// browsed, so it carries entries.
	Problem string `json:"problem"`
	Detail  string `json:"detail"`
}

// RuntimeDirectoryRequest is one listing and how far it has got.
type RuntimeDirectoryRequest struct {
	ID        string `json:"id"`
	RuntimeID string `json:"runtime_id"`
	Status    string `json:"status"`
	Path      string `json:"path"`
	// ProbeWrite asks the daemon to also check that it can write inside Path,
	// which it does by creating a file there. Only asked before saving.
	ProbeWrite bool                     `json:"probe_write"`
	Listing    *RuntimeDirectoryListing `json:"listing,omitempty"`
	Error      string                   `json:"error,omitempty"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`

	claimedAt time.Time
	done      chan struct{}
}

func runtimeDirectoryTerminal(status string) bool {
	return status == runtimeDirectoryCompleted || status == runtimeDirectoryFailed || status == runtimeDirectoryTimeout
}

type runtimeDirectoryStore struct {
	mu       sync.Mutex
	requests map[string]*RuntimeDirectoryRequest
}

func newRuntimeDirectoryStore() *runtimeDirectoryStore {
	return &runtimeDirectoryStore{requests: make(map[string]*RuntimeDirectoryRequest)}
}

var runtimeDirectories = newRuntimeDirectoryStore()

// expire ends a request the daemon did not claim or did not answer in time.
// Callers hold the lock.
func (s *runtimeDirectoryStore) expire(req *RuntimeDirectoryRequest, now time.Time) {
	switch req.Status {
	case runtimeDirectoryPending:
		if now.Sub(req.CreatedAt) > runtimeDirectoryPendingTimeout {
			s.finish(req, runtimeDirectoryTimeout, nil, "the machine did not pick up the request", now)
		}
	case runtimeDirectoryRunning:
		if now.Sub(req.claimedAt) > runtimeDirectoryRunningTimeout {
			s.finish(req, runtimeDirectoryTimeout, nil, "the machine did not answer in time", now)
		}
	}
}

func (s *runtimeDirectoryStore) finish(req *RuntimeDirectoryRequest, status string, listing *RuntimeDirectoryListing, errMsg string, now time.Time) {
	req.Status = status
	req.Listing = listing
	req.Error = errMsg
	req.UpdatedAt = now
	close(req.done)
}

func (s *runtimeDirectoryStore) create(runtimeID, path string, probeWrite bool) (RuntimeDirectoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	open := 0
	for id, req := range s.requests {
		if now.Sub(req.CreatedAt) > runtimeDirectoryRetention {
			delete(s.requests, id)
			continue
		}
		s.expire(req, now)
		if req.RuntimeID == runtimeID && !runtimeDirectoryTerminal(req.Status) {
			open++
		}
	}
	if open >= runtimeDirectoryMaxOpen {
		return RuntimeDirectoryRequest{}, errRuntimeDirectoryBusy
	}
	req := &RuntimeDirectoryRequest{
		ID:         randomID(),
		RuntimeID:  runtimeID,
		Status:     runtimeDirectoryPending,
		Path:       path,
		ProbeWrite: probeWrite,
		CreatedAt:  now,
		UpdatedAt:  now,
		done:       make(chan struct{}),
	}
	s.requests[req.ID] = req
	return *req, nil
}

func (s *runtimeDirectoryStore) get(id string) (RuntimeDirectoryRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return RuntimeDirectoryRequest{}, false
	}
	s.expire(req, time.Now())
	return *req, true
}

// wait returns the request once it has an answer, or as it stands after
// `limit` when it has none yet.
func (s *runtimeDirectoryStore) wait(ctx context.Context, id string, limit time.Duration) (RuntimeDirectoryRequest, bool) {
	req, ok := s.get(id)
	if !ok || runtimeDirectoryTerminal(req.Status) {
		return req, ok
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-req.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return s.get(id)
}

// claim hands every waiting request of a runtime to its daemon, oldest first.
func (s *runtimeDirectoryStore) claim(runtimeID string) []RuntimeDirectoryRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var out []RuntimeDirectoryRequest
	for _, req := range s.requests {
		s.expire(req, now)
		if req.RuntimeID != runtimeID || req.Status != runtimeDirectoryPending {
			continue
		}
		req.Status = runtimeDirectoryRunning
		req.claimedAt = now
		req.UpdatedAt = now
		out = append(out, *req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// report records the daemon's answer. It reports false for a request that is
// unknown or belongs to another runtime; an answer to a request that already
// ended is dropped.
func (s *runtimeDirectoryStore) report(runtimeID, id string, listing *RuntimeDirectoryListing, errMsg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.requests[id]
	if !ok || req.RuntimeID != runtimeID {
		return false
	}
	now := time.Now()
	s.expire(req, now)
	if runtimeDirectoryTerminal(req.Status) {
		return true
	}
	if listing == nil {
		s.finish(req, runtimeDirectoryFailed, nil, errMsg, now)
		return true
	}
	s.finish(req, runtimeDirectoryCompleted, listing, "", now)
	return true
}

type listRuntimeDirectoryRequest struct {
	Path       string `json:"path"`
	ProbeWrite bool   `json:"probe_write"`
}

// InitiateListRuntimeDirectory asks a runtime's machine what is at a path.
// Whoever may run agents on the machine may ask.
func (h *Handler) InitiateListRuntimeDirectory(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	var body listRuntimeDirectoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	path := strings.TrimSpace(body.Path)
	if len(path) > maxRuntimeDirectoryPath || strings.ContainsRune(path, 0) {
		writeError(w, http.StatusBadRequest, "path is not valid")
		return
	}
	if rt.Status != "online" {
		writeError(w, http.StatusServiceUnavailable, "runtime is offline")
		return
	}
	runtimeID := uuidToString(rt.ID)
	req, err := runtimeDirectories.create(runtimeID, path, body.ProbeWrite)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	h.requestDaemonPendingWork(runtimeID, protocol.PendingWorkKindDirectoryListing)
	if answered, ok := runtimeDirectories.wait(r.Context(), req.ID, runtimeDirectoryWait); ok {
		req = answered
	}
	writeJSON(w, http.StatusOK, req)
}

// GetRuntimeDirectoryRequest returns a listing that had no answer yet when it
// was asked for.
func (h *Handler) GetRuntimeDirectoryRequest(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	req, ok := runtimeDirectories.get(chi.URLParam(r, "requestId"))
	if !ok || req.RuntimeID != uuidToString(rt.ID) {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if answered, ok := runtimeDirectories.wait(r.Context(), req.ID, runtimeDirectoryWait); ok {
		req = answered
	}
	writeJSON(w, http.StatusOK, req)
}

type daemonDirectoryListingRequest struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	ProbeWrite bool   `json:"probe_write"`
}

type claimRuntimeDirectoryResponse struct {
	Requests []daemonDirectoryListingRequest `json:"requests"`
}

// ClaimRuntimeDirectoryRequests hands a daemon the listings queued for one of
// its runtimes.
func (h *Handler) ClaimRuntimeDirectoryRequests(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	claimed := runtimeDirectories.claim(uuidToString(rt.ID))
	out := make([]daemonDirectoryListingRequest, 0, len(claimed))
	for _, req := range claimed {
		out = append(out, daemonDirectoryListingRequest{ID: req.ID, Path: req.Path, ProbeWrite: req.ProbeWrite})
	}
	writeJSON(w, http.StatusOK, claimRuntimeDirectoryResponse{Requests: out})
}

type runtimeDirectoryResultRequest struct {
	Listing *RuntimeDirectoryListing `json:"listing"`
	Error   string                   `json:"error"`
}

// ReportRuntimeDirectoryResult records what a daemon found for a claimed
// listing.
func (h *Handler) ReportRuntimeDirectoryResult(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	var body runtimeDirectoryResultRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	errMsg := truncateRunes(strings.TrimSpace(body.Error), maxRuntimeDirectoryDetail)
	if body.Listing == nil && errMsg == "" {
		errMsg = "the machine could not read the directory"
	}
	if !runtimeDirectories.report(uuidToString(rt.ID), chi.URLParam(r, "requestId"), boundRuntimeDirectoryListing(body.Listing), errMsg) {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// boundRuntimeDirectoryListing keeps a daemon's answer within what the page is
// prepared to show.
func boundRuntimeDirectoryListing(listing *RuntimeDirectoryListing) *RuntimeDirectoryListing {
	if listing == nil {
		return nil
	}
	out := *listing
	out.Path = truncateRunes(out.Path, maxRuntimeDirectoryPath)
	out.Detail = truncateRunes(out.Detail, maxRuntimeDirectoryDetail)
	if out.Separator != `\` {
		out.Separator = "/"
	}
	entries := make([]string, 0, min(len(out.Entries), maxRuntimeDirectoryEntries))
	for _, name := range out.Entries {
		if len(entries) == maxRuntimeDirectoryEntries {
			out.Truncated = true
			break
		}
		if name == "" || len(name) > maxRuntimeDirectoryName {
			continue
		}
		entries = append(entries, name)
	}
	out.Entries = entries
	return &out
}
