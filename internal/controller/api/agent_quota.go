package api

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type agentQuotaKind string

const (
	agentQuotaProbeTargets agentQuotaKind = "probe-targets"
	agentQuotaProbeResults agentQuotaKind = "probe-results"
	agentQuotaHeartbeat    agentQuotaKind = "heartbeat"
	agentQuotaHost         agentQuotaKind = "host"
	agentQuotaState        agentQuotaKind = "state"
	agentQuotaPresence     agentQuotaKind = "presence"

	agentWriteMaxConcurrent           = 4
	agentWriteMaxGlobalConcurrent     = 64
	agentPresenceMaxConcurrentPerNode = 2
	agentQuotaIdleRetention           = time.Hour
	agentQuotaPruneInterval           = 5 * time.Minute
	agentAuthMaxConcurrent            = 64
	agentAuthMaxKeys                  = 4096
	agentAuthIdleRetention            = 10 * time.Minute
)

type agentBucketSpec struct {
	refillPerSecond float64
	burst           float64
}

var agentRequestBucketSpecs = map[agentQuotaKind]agentBucketSpec{
	// State explicitly supports a one-second reporting cadence plus a modest
	// retry/startup burst.
	agentQuotaState: {refillPerSecond: 1.25, burst: 6},
	// Probe result posts can run every five seconds. The bounded burst covers stale
	// config rejection, refresh, retry and a short reconnect sequence without
	// changing the sustained rate.
	agentQuotaProbeResults: {refillPerSecond: 0.4, burst: 8},
	agentQuotaHeartbeat:    {refillPerSecond: 0.2, burst: 4},
	agentQuotaHost:         {refillPerSecond: 1.0 / 30.0, burst: 2},
	agentQuotaProbeTargets: {refillPerSecond: 0.25, burst: 5},
	agentQuotaPresence:     {refillPerSecond: 0.2, burst: 4},
}

var agentWriteBucketSpec = agentBucketSpec{refillPerSecond: 5, burst: 24}

var (
	agentAuthGlobalBucketSpec = agentBucketSpec{refillPerSecond: 100, burst: 200}
	agentAuthPerIPBucketSpec  = agentBucketSpec{refillPerSecond: 20, burst: 40}
)

type agentTokenBucket struct {
	tokens    float64
	updatedAt time.Time
}

type agentNodeQuota struct {
	buckets          map[agentQuotaKind]*agentTokenBucket
	writeBucket      agentTokenBucket
	writesInFlight   int
	presenceInFlight int
	lastSeen         time.Time
}

type agentQuotaManager struct {
	mu             sync.Mutex
	nodes          map[string]*agentNodeQuota
	writesInFlight int
	now            func() time.Time
	lastPruned     time.Time
}

type agentAuthAdmissionEntry struct {
	bucket   agentTokenBucket
	lastSeen time.Time
}

// agentAuthAdmissionManager bounds the database work performed before an
// Agent credential has been authenticated. It is deliberately independent of
// the per-node quota manager: unauthenticated callers must not be able to
// create arbitrary node quota entries or bypass a limit by rotating node IDs.
type agentAuthAdmissionManager struct {
	mu       sync.Mutex
	entries  map[string]*agentAuthAdmissionEntry
	global   agentTokenBucket
	inFlight int
	now      func() time.Time
}

func newAgentAuthAdmissionManager() *agentAuthAdmissionManager {
	return &agentAuthAdmissionManager{entries: make(map[string]*agentAuthAdmissionEntry), now: time.Now}
}

func (manager *agentAuthAdmissionManager) admit(key string) (func(), time.Duration, bool) {
	if manager == nil {
		return func() {}, 0, true
	}
	now := time.Now().UTC()
	if manager.now != nil {
		now = manager.now().UTC()
	}
	if key == "" {
		key = "unknown"
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked(now)
	if manager.inFlight >= agentAuthMaxConcurrent {
		return nil, time.Second, false
	}
	entry := manager.entries[key]
	isNewEntry := entry == nil
	if entry == nil {
		if len(manager.entries) >= agentAuthMaxKeys {
			return nil, time.Second, false
		}
		entry = &agentAuthAdmissionEntry{}
	}
	// Refill and validate both buckets before consuming either one. A denied
	// per-IP request must not drain the shared global bucket, otherwise one
	// abusive source could temporarily prevent every healthy Agent from
	// authenticating.
	if retryAfter, ok := availableAgentBucket(&entry.bucket, agentAuthPerIPBucketSpec, 1, now); !ok {
		return nil, retryAfter, false
	}
	if retryAfter, ok := availableAgentBucket(&manager.global, agentAuthGlobalBucketSpec, 1, now); !ok {
		return nil, retryAfter, false
	}
	if isNewEntry {
		manager.entries[key] = entry
	}
	entry.lastSeen = now
	entry.bucket.tokens--
	manager.global.tokens--
	manager.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			if manager.inFlight > 0 {
				manager.inFlight--
			}
			if current := manager.entries[key]; current != nil {
				current.lastSeen = time.Now().UTC()
				if manager.now != nil {
					current.lastSeen = manager.now().UTC()
				}
			}
			manager.mu.Unlock()
		})
	}, 0, true
}

func availableAgentBucket(bucket *agentTokenBucket, spec agentBucketSpec, cost float64, now time.Time) (time.Duration, bool) {
	refillAgentBucket(bucket, spec, now)
	if cost <= 0 || bucket.tokens >= cost {
		return 0, true
	}
	if spec.refillPerSecond <= 0 {
		return time.Hour, false
	}
	waitSeconds := (cost - bucket.tokens) / spec.refillPerSecond
	if waitSeconds < 1 {
		waitSeconds = 1
	}
	return time.Duration(math.Ceil(waitSeconds)) * time.Second, false
}

func (manager *agentAuthAdmissionManager) pruneLocked(now time.Time) {
	if len(manager.entries) < agentAuthMaxKeys {
		return
	}
	for key, entry := range manager.entries {
		if now.Sub(entry.lastSeen) > agentAuthIdleRetention {
			delete(manager.entries, key)
		}
	}
}

func newAgentQuotaManager() *agentQuotaManager {
	return &agentQuotaManager{nodes: make(map[string]*agentNodeQuota), now: time.Now}
}

func (manager *agentQuotaManager) currentTime() time.Time {
	if manager != nil && manager.now != nil {
		return manager.now().UTC()
	}
	return time.Now().UTC()
}

func refillAgentBucket(bucket *agentTokenBucket, spec agentBucketSpec, now time.Time) {
	if bucket.updatedAt.IsZero() {
		bucket.tokens = spec.burst
		bucket.updatedAt = now
		return
	}
	elapsed := now.Sub(bucket.updatedAt).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(spec.burst, bucket.tokens+elapsed*spec.refillPerSecond)
		bucket.updatedAt = now
	}
}

func takeAgentBucket(bucket *agentTokenBucket, spec agentBucketSpec, cost float64, now time.Time) (time.Duration, bool) {
	refillAgentBucket(bucket, spec, now)
	if cost <= 0 {
		return 0, true
	}
	if bucket.tokens >= cost {
		bucket.tokens -= cost
		return 0, true
	}
	if spec.refillPerSecond <= 0 {
		return time.Hour, false
	}
	waitSeconds := (cost - bucket.tokens) / spec.refillPerSecond
	if waitSeconds < 1 {
		waitSeconds = 1
	}
	return time.Duration(math.Ceil(waitSeconds)) * time.Second, false
}

func (manager *agentQuotaManager) nodeLocked(nodeID string, now time.Time) *agentNodeQuota {
	quota := manager.nodes[nodeID]
	if quota == nil {
		quota = &agentNodeQuota{buckets: make(map[agentQuotaKind]*agentTokenBucket)}
		manager.nodes[nodeID] = quota
	}
	quota.lastSeen = now
	if manager.lastPruned.IsZero() || now.Sub(manager.lastPruned) >= agentQuotaPruneInterval {
		for key, candidate := range manager.nodes {
			if candidate.writesInFlight == 0 && candidate.presenceInFlight == 0 && now.Sub(candidate.lastSeen) > agentQuotaIdleRetention {
				delete(manager.nodes, key)
			}
		}
		manager.lastPruned = now
	}
	return quota
}

func (manager *agentQuotaManager) admitRequest(nodeID string, kind agentQuotaKind) (time.Duration, bool) {
	if manager == nil {
		return 0, true
	}
	spec, ok := agentRequestBucketSpecs[kind]
	if !ok {
		return 0, true
	}
	now := manager.currentTime()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	quota := manager.nodeLocked(nodeID, now)
	bucket := quota.buckets[kind]
	if bucket == nil {
		bucket = &agentTokenBucket{}
		quota.buckets[kind] = bucket
	}
	return takeAgentBucket(bucket, spec, 1, now)
}

func (manager *agentQuotaManager) admitWrite(nodeID string, units float64) (func(), time.Duration, bool) {
	if manager == nil {
		return func() {}, 0, true
	}
	now := manager.currentTime()
	manager.mu.Lock()
	quota := manager.nodeLocked(nodeID, now)
	if manager.writesInFlight >= agentWriteMaxGlobalConcurrent {
		manager.mu.Unlock()
		return nil, time.Second, false
	}
	if quota.writesInFlight >= agentWriteMaxConcurrent {
		manager.mu.Unlock()
		return nil, time.Second, false
	}
	if retryAfter, ok := takeAgentBucket(&quota.writeBucket, agentWriteBucketSpec, units, now); !ok {
		manager.mu.Unlock()
		return nil, retryAfter, false
	}
	quota.writesInFlight++
	manager.writesInFlight++
	manager.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			if manager.writesInFlight > 0 {
				manager.writesInFlight--
			}
			if current := manager.nodes[nodeID]; current != nil && current.writesInFlight > 0 {
				current.writesInFlight--
				current.lastSeen = manager.currentTime()
			}
			manager.mu.Unlock()
		})
	}, 0, true
}

func (manager *agentQuotaManager) acquirePresence(nodeID string) (func(), time.Duration, bool) {
	if manager == nil {
		return func() {}, 0, true
	}
	now := manager.currentTime()
	manager.mu.Lock()
	quota := manager.nodeLocked(nodeID, now)
	if quota.presenceInFlight >= agentPresenceMaxConcurrentPerNode {
		manager.mu.Unlock()
		return nil, time.Second, false
	}
	quota.presenceInFlight++
	manager.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			if current := manager.nodes[nodeID]; current != nil && current.presenceInFlight > 0 {
				current.presenceInFlight--
				current.lastSeen = manager.currentTime()
			}
			manager.mu.Unlock()
		})
	}, 0, true
}

func writeAgentRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
}

func (h *handler) admitAgentRequest(w http.ResponseWriter, nodeID string, kind agentQuotaKind) bool {
	retryAfter, ok := h.agentQuotas.admitRequest(nodeID, kind)
	if !ok {
		writeAgentRateLimit(w, retryAfter)
		return false
	}
	return true
}

func (h *handler) admitAgentWrite(w http.ResponseWriter, nodeID string, units float64) (func(), bool) {
	release, retryAfter, ok := h.agentQuotas.admitWrite(nodeID, units)
	if !ok {
		writeAgentRateLimit(w, retryAfter)
		return nil, false
	}
	return release, true
}

func agentProbeWriteUnits(request AgentProbeResultsRequest) float64 {
	sampleCount := 0
	for _, round := range request.Rounds {
		sampleCount += len(round.Samples)
	}
	// One transaction plus bounded round/sample work. A maximum legitimate
	// 32x32 batch costs 14 units and fits the burst, then refills within the
	// supported five-second minimum probe interval alongside one-second state.
	return 2 + math.Ceil(float64(len(request.Rounds))/8) + math.Ceil(float64(sampleCount)/128)
}
