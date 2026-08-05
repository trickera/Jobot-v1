package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// llmCacheTTL is how long a provider answer stays reusable. Every Resume Studio
// prompt is deterministic (temperature 0), so the same resume + the same job
// yields the same answer; re-asking only spends the user's free-tier quota.
// A few hours is long enough to cover a working session and short enough that
// a model or provider improvement is picked up on the next run.
const llmCacheTTL = 6 * time.Hour

// llmCacheMaxEntries bounds memory. Prompts carry whole resumes, so entries are
// measured in kilobytes; a few dozen is more than one session ever needs.
const llmCacheMaxEntries = 64

type llmCacheEntry struct {
	result    cascadeResult
	expiresAt time.Time
}

// llmCache memoizes cascade results so that re-running gap analysis or
// tailoring against an unchanged resume and job is instant and free, and so a
// user who hits a rate limit on one step does not have to re-pay for the steps
// that already succeeded.
//
// It is two layers. The map is the fast path within a session; the store is what
// makes the cache worth having at all, because an answer bought with free-tier
// quota should not evaporate the moment the user closes the app. A nil store
// degrades to memory-only, which is what tests want.
type llmCache struct {
	mu      sync.Mutex
	entries map[string]llmCacheEntry
	store   *configStore
}

func newLLMCache(store *configStore) *llmCache {
	return &llmCache{entries: map[string]llmCacheEntry{}, store: store}
}

// llmCacheKey identifies an answer by what actually determines it: the task,
// the provider/model that will be asked, and the exact prompt. Provider and
// model are part of the key so that switching either in Settings genuinely
// re-asks instead of replaying the previous provider's answer.
func llmCacheKey(purpose llmPurpose, provider, model, prompt string) string {
	sum := sha256.New()
	for _, part := range []string{string(purpose), provider, model, prompt} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func (c *llmCache) get(key string, now time.Time) (cascadeResult, bool) {
	if c == nil {
		return cascadeResult{}, false
	}

	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok && now.After(entry.expiresAt) {
		delete(c.entries, key)
		ok = false
	}
	c.mu.Unlock()
	if ok {
		return entry.result, true
	}

	// Missed in memory: this may still be an answer a previous session paid for.
	raw, found := c.store.llmCacheGet(key, now)
	if !found {
		return cascadeResult{}, false
	}
	var result cascadeResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return cascadeResult{}, false
	}

	c.mu.Lock()
	c.evictLocked(now)
	c.entries[key] = llmCacheEntry{result: result, expiresAt: now.Add(llmCacheTTL)}
	c.mu.Unlock()
	return result, true
}

func (c *llmCache) put(key string, result cascadeResult, now time.Time) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.evictLocked(now)
	c.entries[key] = llmCacheEntry{result: result, expiresAt: now.Add(llmCacheTTL)}
	c.mu.Unlock()

	if encoded, err := json.Marshal(result); err == nil {
		c.store.llmCachePut(key, string(encoded), now.Add(llmCacheTTL))
	}
}

// evictLocked drops expired entries first and only then falls back to dropping
// the entry closest to expiry, which for a fixed TTL is the oldest one.
func (c *llmCache) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	for len(c.entries) >= llmCacheMaxEntries {
		oldestKey := ""
		var oldestAt time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldestAt) {
				oldestKey, oldestAt = key, entry.expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
}
