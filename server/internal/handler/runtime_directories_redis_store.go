package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed implementation of RuntimeDirectoryStore. The layout matches
// runtime_models_redis_store.go, which solves the same multi-node dispatch
// problem: namespaced keys, a ZSET-backed pending queue and an atomic claim
// through the shared Lua script.
//
// Key layout:
//
//	mul:{runtime_pending}:directory:req:<request_id>      → JSON envelope of the request, TTL = retention
//	mul:{runtime_pending}:directory:pending:<runtime_id>  → ZSET { member = request_id, score = created_at UnixNano }
//	                                                        TTL = retention*2, so stale members are swept on Claim

const (
	runtimeDirectoryKeyPrefix     = "mul:" + runtimePendingRedisHashTag + ":directory:req:"
	runtimeDirectoryPendingPrefix = "mul:" + runtimePendingRedisHashTag + ":directory:pending:"
	// A waiting read asks again this often; there is no cross-node signal.
	runtimeDirectoryRedisPoll = 100 * time.Millisecond
)

func runtimeDirectoryKey(id string) string { return runtimeDirectoryKeyPrefix + id }
func runtimeDirectoryPendingKey(runtimeID string) string {
	return runtimeDirectoryPendingPrefix + runtimeID
}

// RedisRuntimeDirectoryStore keeps directory listings in Redis so the page's
// read, the daemon's claim and its report agree whichever API node each one
// reaches.
type RedisRuntimeDirectoryStore struct {
	rdb redis.UniversalClient
}

func NewRedisRuntimeDirectoryStore(rdb redis.UniversalClient) *RedisRuntimeDirectoryStore {
	return &RedisRuntimeDirectoryStore{rdb: rdb}
}

// redisRuntimeDirectoryEnvelope carries ClaimedAt, which the request keeps out
// of its JSON so it does not reach the HTTP response. Without it no node could
// tell how long a claimed request has been running.
type redisRuntimeDirectoryEnvelope struct {
	Public    *RuntimeDirectoryRequest `json:"r"`
	ClaimedAt *time.Time               `json:"c,omitempty"`
}

func (s *RedisRuntimeDirectoryStore) marshalRequest(req *RuntimeDirectoryRequest) ([]byte, error) {
	data, err := json.Marshal(redisRuntimeDirectoryEnvelope{Public: req, ClaimedAt: req.ClaimedAt})
	if err != nil {
		return nil, fmt.Errorf("marshal directory listing: %w", err)
	}
	return data, nil
}

func (s *RedisRuntimeDirectoryStore) unmarshalRequest(raw []byte) (*RuntimeDirectoryRequest, error) {
	var env redisRuntimeDirectoryEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode directory listing: %w", err)
	}
	if env.Public == nil {
		return nil, fmt.Errorf("decode directory listing: missing payload")
	}
	env.Public.ClaimedAt = env.ClaimedAt
	return env.Public, nil
}

func (s *RedisRuntimeDirectoryStore) persistRequest(ctx context.Context, req *RuntimeDirectoryRequest) error {
	data, err := s.marshalRequest(req)
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, runtimeDirectoryKey(req.ID), data, runtimeDirectoryRetention).Err(); err != nil {
		return fmt.Errorf("persist directory listing: %w", err)
	}
	return nil
}

// loadRequest fetches a request and applies its timeout, persisting the change
// so every node sees the same terminal state.
func (s *RedisRuntimeDirectoryStore) loadRequest(ctx context.Context, id string) (*RuntimeDirectoryRequest, error) {
	raw, err := s.rdb.Get(ctx, runtimeDirectoryKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get directory listing: %w", err)
	}
	req, err := s.unmarshalRequest(raw)
	if err != nil {
		return nil, err
	}
	if applyRuntimeDirectoryTimeout(req, time.Now()) {
		if err := s.persistRequest(ctx, req); err != nil {
			return nil, err
		}
		s.rdb.ZRem(ctx, runtimeDirectoryPendingKey(req.RuntimeID), req.ID)
	}
	return req, nil
}

// Create bounds a runtime by the requests still waiting to be claimed. Claimed
// ones are not counted: a daemon answers them, or they time out, within
// seconds.
func (s *RedisRuntimeDirectoryStore) Create(ctx context.Context, runtimeID, path string, probeWrite bool) (*RuntimeDirectoryRequest, error) {
	pendingKey := runtimeDirectoryPendingKey(runtimeID)
	waiting, err := s.rdb.ZCard(ctx, pendingKey).Result()
	if err != nil {
		return nil, fmt.Errorf("zcard pending: %w", err)
	}
	if waiting >= runtimeDirectoryMaxOpen {
		// The queue may hold requests that have already timed out; a claim or
		// a read sweeps them. Sweep here too before refusing.
		if waiting, err = s.sweepPending(ctx, runtimeID); err != nil {
			return nil, err
		}
		if waiting >= runtimeDirectoryMaxOpen {
			return nil, errRuntimeDirectoryBusy
		}
	}

	now := time.Now()
	req := &RuntimeDirectoryRequest{
		ID:         randomID(),
		RuntimeID:  runtimeID,
		Status:     runtimeDirectoryPending,
		Path:       path,
		ProbeWrite: probeWrite,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	data, err := s.marshalRequest(req)
	if err != nil {
		return nil, err
	}
	requestKey := runtimeDirectoryKey(req.ID)
	// A plain pipeline, as for model lists: the request is not visible to a
	// claim until it is in the pending set, and managed Redis may deny MULTI.
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, requestKey, data, runtimeDirectoryRetention)
	pipe.ZAdd(ctx, pendingKey, redis.Z{Score: float64(now.UnixNano()), Member: req.ID})
	pipe.Expire(ctx, pendingKey, runtimeDirectoryRetention*2)
	if _, err := pipe.Exec(ctx); err != nil {
		_ = s.rdb.Del(ctx, requestKey).Err()
		_ = s.rdb.ZRem(ctx, pendingKey, req.ID).Err()
		return nil, fmt.Errorf("persist directory listing: %w", err)
	}
	return req, nil
}

// sweepPending drops the members of a runtime's queue that are gone or no
// longer pending, and returns how many remain.
func (s *RedisRuntimeDirectoryStore) sweepPending(ctx context.Context, runtimeID string) (int64, error) {
	pendingKey := runtimeDirectoryPendingKey(runtimeID)
	ids, err := s.rdb.ZRange(ctx, pendingKey, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("zrange pending: %w", err)
	}
	var remaining int64
	for _, id := range ids {
		req, err := s.loadRequest(ctx, id)
		if err != nil {
			return 0, err
		}
		if req == nil || req.Status != runtimeDirectoryPending {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		remaining++
	}
	return remaining, nil
}

func (s *RedisRuntimeDirectoryStore) Get(ctx context.Context, id string) (*RuntimeDirectoryRequest, error) {
	return s.loadRequest(ctx, id)
}

func (s *RedisRuntimeDirectoryStore) Wait(ctx context.Context, id string, limit time.Duration) (*RuntimeDirectoryRequest, error) {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	poll := time.NewTicker(runtimeDirectoryRedisPoll)
	defer poll.Stop()
	for {
		req, err := s.loadRequest(ctx, id)
		if err != nil || req == nil || runtimeDirectoryTerminal(req.Status) {
			return req, err
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			return req, nil
		case <-ctx.Done():
			return req, nil
		}
	}
}

func (s *RedisRuntimeDirectoryStore) Claim(ctx context.Context, runtimeID string) ([]RuntimeDirectoryRequest, error) {
	pendingKey := runtimeDirectoryPendingKey(runtimeID)
	// Scores are creation times, so the range is already oldest first.
	ids, err := s.rdb.ZRange(ctx, pendingKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange pending: %w", err)
	}
	var out []RuntimeDirectoryRequest
	for _, id := range ids {
		req, err := s.loadRequest(ctx, id)
		if err != nil {
			return out, err
		}
		if req == nil || req.Status != runtimeDirectoryPending {
			// Expired, timed out, or taken by another node.
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		now := time.Now()
		req.Status = runtimeDirectoryRunning
		req.ClaimedAt = &now
		req.UpdatedAt = now
		data, err := s.marshalRequest(req)
		if err != nil {
			return out, err
		}
		won, err := claimPendingScript.Run(
			ctx, s.rdb,
			[]string{pendingKey, runtimeDirectoryKey(id)},
			id, data, int(runtimeDirectoryRetention.Seconds()),
		).Int64()
		if err != nil {
			return out, fmt.Errorf("claim pending: %w", err)
		}
		if won == 0 {
			// Another node claimed it between the read and the script.
			continue
		}
		out = append(out, *req)
	}
	return out, nil
}

func (s *RedisRuntimeDirectoryStore) Report(ctx context.Context, runtimeID, id string, listing *RuntimeDirectoryListing, errMsg string) (bool, error) {
	req, err := s.loadRequest(ctx, id)
	if err != nil {
		return false, err
	}
	if req == nil || req.RuntimeID != runtimeID {
		return false, nil
	}
	if runtimeDirectoryTerminal(req.Status) {
		return true, nil
	}
	answerRuntimeDirectory(req, listing, errMsg, time.Now())
	if err := s.persistRequest(ctx, req); err != nil {
		return false, err
	}
	return true, nil
}
