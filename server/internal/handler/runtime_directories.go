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
//
// Requests live in a RuntimeDirectoryStore. The in-memory one serves a single
// API node; with Redis configured every node shares one, so the page's read,
// the daemon's claim and its report may each reach a different node.

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

	// ClaimedAt is when a daemon took the request. It is server bookkeeping
	// for the running timeout and is not part of the response; a shared store
	// has to persist it separately (see redisRuntimeDirectoryEnvelope).
	ClaimedAt *time.Time `json:"-"`
}

func runtimeDirectoryTerminal(status string) bool {
	return status == runtimeDirectoryCompleted || status == runtimeDirectoryFailed || status == runtimeDirectoryTimeout
}

// RuntimeDirectoryStore is the contract both backends satisfy: in-memory for a
// single node, Redis for several. Get and Wait return nil for an unknown
// request.
type RuntimeDirectoryStore interface {
	// Create queues a listing for a runtime. It returns
	// errRuntimeDirectoryBusy when the runtime already has too many waiting.
	Create(ctx context.Context, runtimeID, path string, probeWrite bool) (*RuntimeDirectoryRequest, error)
	Get(ctx context.Context, id string) (*RuntimeDirectoryRequest, error)
	// Wait returns the request once it has an answer, or as it stands after
	// limit when it has none yet.
	Wait(ctx context.Context, id string, limit time.Duration) (*RuntimeDirectoryRequest, error)
	// Claim hands every waiting request of a runtime to its daemon, oldest
	// first. No request is handed out twice.
	Claim(ctx context.Context, runtimeID string) ([]RuntimeDirectoryRequest, error)
	// Report records the daemon's answer. It reports false for a request that
	// is unknown or belongs to another runtime; an answer to a request that
	// already ended is dropped.
	Report(ctx context.Context, runtimeID, id string, listing *RuntimeDirectoryListing, errMsg string) (bool, error)
}

// applyRuntimeDirectoryTimeout ends a request the daemon did not claim or did
// not answer in time. It reports whether the request changed, so a shared
// store knows to persist it.
func applyRuntimeDirectoryTimeout(req *RuntimeDirectoryRequest, now time.Time) bool {
	switch req.Status {
	case runtimeDirectoryPending:
		if now.Sub(req.CreatedAt) > runtimeDirectoryPendingTimeout {
			finishRuntimeDirectory(req, runtimeDirectoryTimeout, nil, "the machine did not pick up the request", now)
			return true
		}
	case runtimeDirectoryRunning:
		if req.ClaimedAt != nil && now.Sub(*req.ClaimedAt) > runtimeDirectoryRunningTimeout {
			finishRuntimeDirectory(req, runtimeDirectoryTimeout, nil, "the machine did not answer in time", now)
			return true
		}
	}
	return false
}

func finishRuntimeDirectory(req *RuntimeDirectoryRequest, status string, listing *RuntimeDirectoryListing, errMsg string, now time.Time) {
	req.Status = status
	req.Listing = listing
	req.Error = errMsg
	req.UpdatedAt = now
}

// answerRuntimeDirectory applies a daemon's report to a request that is still
// open.
func answerRuntimeDirectory(req *RuntimeDirectoryRequest, listing *RuntimeDirectoryListing, errMsg string, now time.Time) {
	if listing == nil {
		finishRuntimeDirectory(req, runtimeDirectoryFailed, nil, errMsg, now)
		return
	}
	finishRuntimeDirectory(req, runtimeDirectoryCompleted, listing, "", now)
}

// InMemoryRuntimeDirectoryStore is the single-node implementation. A waiting
// read is released by the report itself rather than by polling.
type InMemoryRuntimeDirectoryStore struct {
	mu       sync.Mutex
	requests map[string]*RuntimeDirectoryRequest
	// answered is closed when its request reaches a terminal status.
	answered map[string]chan struct{}
}

func NewInMemoryRuntimeDirectoryStore() *InMemoryRuntimeDirectoryStore {
	return &InMemoryRuntimeDirectoryStore{
		requests: make(map[string]*RuntimeDirectoryRequest),
		answered: make(map[string]chan struct{}),
	}
}

// settle releases the waiters of a request that has just ended. Callers hold
// the lock.
func (s *InMemoryRuntimeDirectoryStore) settle(req *RuntimeDirectoryRequest, changed bool) {
	if changed && runtimeDirectoryTerminal(req.Status) {
		close(s.answered[req.ID])
	}
}

func (s *InMemoryRuntimeDirectoryStore) Create(_ context.Context, runtimeID, path string, probeWrite bool) (*RuntimeDirectoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	open := 0
	for id, req := range s.requests {
		if now.Sub(req.CreatedAt) > runtimeDirectoryRetention {
			delete(s.requests, id)
			delete(s.answered, id)
			continue
		}
		s.settle(req, applyRuntimeDirectoryTimeout(req, now))
		if req.RuntimeID == runtimeID && !runtimeDirectoryTerminal(req.Status) {
			open++
		}
	}
	if open >= runtimeDirectoryMaxOpen {
		return nil, errRuntimeDirectoryBusy
	}
	req := &RuntimeDirectoryRequest{
		ID:         randomID(),
		RuntimeID:  runtimeID,
		Status:     runtimeDirectoryPending,
		Path:       path,
		ProbeWrite: probeWrite,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.requests[req.ID] = req
	s.answered[req.ID] = make(chan struct{})
	out := *req
	return &out, nil
}

func (s *InMemoryRuntimeDirectoryStore) Get(_ context.Context, id string) (*RuntimeDirectoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	s.settle(req, applyRuntimeDirectoryTimeout(req, time.Now()))
	out := *req
	return &out, nil
}

func (s *InMemoryRuntimeDirectoryStore) Wait(ctx context.Context, id string, limit time.Duration) (*RuntimeDirectoryRequest, error) {
	req, err := s.Get(ctx, id)
	if err != nil || req == nil || runtimeDirectoryTerminal(req.Status) {
		return req, err
	}
	s.mu.Lock()
	answered := s.answered[id]
	s.mu.Unlock()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-answered:
	case <-timer.C:
	case <-ctx.Done():
	}
	return s.Get(ctx, id)
}

func (s *InMemoryRuntimeDirectoryStore) Claim(_ context.Context, runtimeID string) ([]RuntimeDirectoryRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var out []RuntimeDirectoryRequest
	for _, req := range s.requests {
		s.settle(req, applyRuntimeDirectoryTimeout(req, now))
		if req.RuntimeID != runtimeID || req.Status != runtimeDirectoryPending {
			continue
		}
		req.Status = runtimeDirectoryRunning
		req.ClaimedAt = &now
		req.UpdatedAt = now
		out = append(out, *req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *InMemoryRuntimeDirectoryStore) Report(_ context.Context, runtimeID, id string, listing *RuntimeDirectoryListing, errMsg string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.requests[id]
	if !ok || req.RuntimeID != runtimeID {
		return false, nil
	}
	now := time.Now()
	s.settle(req, applyRuntimeDirectoryTimeout(req, now))
	if runtimeDirectoryTerminal(req.Status) {
		return true, nil
	}
	answerRuntimeDirectory(req, listing, errMsg, now)
	s.settle(req, true)
	return true, nil
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
	req, err := h.RuntimeDirectoryStore.Create(r.Context(), runtimeID, path, body.ProbeWrite)
	if errors.Is(err, errRuntimeDirectoryBusy) {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue the directory listing")
		return
	}
	h.requestDaemonPendingWork(runtimeID, protocol.PendingWorkKindDirectoryListing)
	// A failed wait still leaves the queued request to answer with.
	if answered, err := h.RuntimeDirectoryStore.Wait(r.Context(), req.ID, runtimeDirectoryWait); err == nil && answered != nil {
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
	req, err := h.RuntimeDirectoryStore.Get(r.Context(), chi.URLParam(r, "requestId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the directory listing")
		return
	}
	if req == nil || req.RuntimeID != uuidToString(rt.ID) {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	if answered, err := h.RuntimeDirectoryStore.Wait(r.Context(), req.ID, runtimeDirectoryWait); err == nil && answered != nil {
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
	claimed, err := h.RuntimeDirectoryStore.Claim(r.Context(), uuidToString(rt.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to claim directory listings")
		return
	}
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
	known, err := h.RuntimeDirectoryStore.Report(r.Context(), uuidToString(rt.ID), chi.URLParam(r, "requestId"), boundRuntimeDirectoryListing(body.Listing), errMsg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the directory listing")
		return
	}
	if !known {
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
