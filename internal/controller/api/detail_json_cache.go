package api

import (
	"context"
	"sync"
	"time"
)

type detailJSONCacheEntry struct {
	payload   []byte
	updatedAt time.Time
}

// jsonCacheFlight is shared by the detail and full-summary caches. A flight is
// tied to the cache generation it started in so invalidation can prevent an
// older result from being committed.
type jsonCacheFlight struct {
	generation uint64
	done       chan struct{}
	payload    []byte
	err        error
}

type detailJSONCache struct {
	mu          sync.Mutex
	entries     map[string]detailJSONCacheEntry
	flights     map[string]*jsonCacheFlight
	generations map[string]uint64
}

func newDetailJSONCache() *detailJSONCache {
	return &detailJSONCache{
		entries:     make(map[string]detailJSONCacheEntry),
		flights:     make(map[string]*jsonCacheFlight),
		generations: make(map[string]uint64),
	}
}

func (cache *detailJSONCache) get(ctx context.Context, key string, maxAge time.Duration, load func() ([]byte, error)) ([]byte, error) {
	if cache == nil {
		return load()
	}
	for {
		now := time.Now()
		cache.mu.Lock()
		cache.pruneExpiredLocked(now, maxAge)
		generation := cache.generations[key]
		if entry, ok := cache.entries[key]; ok && maxAge > 0 && now.Sub(entry.updatedAt) <= maxAge {
			payload := append([]byte(nil), entry.payload...)
			cache.mu.Unlock()
			return payload, nil
		}
		if flight := cache.flights[key]; flight != nil && flight.generation == generation {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			cache.mu.Lock()
			currentGeneration := cache.generations[key]
			cache.mu.Unlock()
			if currentGeneration != flight.generation {
				continue
			}
			return append([]byte(nil), flight.payload...), flight.err
		}
		flight := &jsonCacheFlight{generation: generation, done: make(chan struct{})}
		cache.flights[key] = flight
		cache.mu.Unlock()

		payload, err := load()
		cache.mu.Lock()
		currentGeneration := cache.generations[key]
		if err == nil && currentGeneration == generation {
			payload = append([]byte(nil), payload...)
			cache.entries[key] = detailJSONCacheEntry{payload: payload, updatedAt: time.Now()}
		}
		flight.payload = payload
		flight.err = err
		if cache.flights[key] == flight {
			delete(cache.flights, key)
		}
		close(flight.done)
		cache.mu.Unlock()
		if currentGeneration != generation {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		return append([]byte(nil), payload...), err
	}
}

func (cache *detailJSONCache) refresh(key string, load func() ([]byte, error)) ([]byte, error) {
	if cache == nil {
		return load()
	}
	cache.mu.Lock()
	cache.generations[key]++
	generation := cache.generations[key]
	delete(cache.entries, key)
	cache.mu.Unlock()

	payload, err := load()
	cache.mu.Lock()
	if err == nil && cache.generations[key] == generation {
		payload = append([]byte(nil), payload...)
		cache.entries[key] = detailJSONCacheEntry{payload: payload, updatedAt: time.Now()}
	}
	cache.mu.Unlock()
	return append([]byte(nil), payload...), err
}

func (cache *detailJSONCache) evict(key string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	delete(cache.entries, key)
	if cache.flights[key] != nil {
		cache.generations[key]++
	} else {
		delete(cache.generations, key)
	}
	cache.mu.Unlock()
}

func (cache *detailJSONCache) pruneExpiredLocked(now time.Time, maxAge time.Duration) {
	if maxAge <= 0 {
		return
	}
	for key, entry := range cache.entries {
		if now.Sub(entry.updatedAt) <= maxAge {
			continue
		}
		delete(cache.entries, key)
		if cache.flights[key] == nil {
			delete(cache.generations, key)
		}
	}
}
