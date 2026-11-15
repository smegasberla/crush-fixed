package speed

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// ResultCache answers a repeated tool call from the value the first one
// produced, so a redundant call costs a map lookup instead of a round trip.
//
// It is per-turn and mutation-invalidated: any edit or command that writes
// clears the cache, because the tool's answer can no longer be assumed. That
// rule is what makes the cache safe — a read cache that survives a write is how
// an agent ends up confidently describing a file that no longer exists.
type ResultCache struct {
	mu sync.Mutex
	// entries is keyed by the tool name plus its arguments.
	entries map[string]string
	// stats are the counters a caller reports.
	stats Stats
}

// Stats reports what a cache did.
type Stats struct {
	// Hits is how many calls were answered from the cache.
	Hits int
	// Misses is how many were not.
	Misses int
	// Invalidations is how many times a mutation cleared the cache.
	Invalidations int
	// CharsAvoided is how many characters of tool output were never re-sent
	// because they came from the cache.
	CharsAvoided int
}

// CacheKey builds the key for one tool call. Two calls share a key only when
// both the tool and its arguments are identical, which is exactly the case
// where the second result cannot differ.
func CacheKey(toolName, arguments string) string {
	sum := sha256.Sum256([]byte(toolName + "\x00" + arguments))
	return hex.EncodeToString(sum[:16])
}

// NewResultCache returns an empty cache.
func NewResultCache() *ResultCache {
	return &ResultCache{entries: make(map[string]string)}
}

// Lookup returns a cached result for the key.
func (c *ResultCache) Lookup(key string) (string, bool) {
	if c == nil || key == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if !ok {
		c.stats.Misses++
		return "", false
	}
	c.stats.Hits++
	c.stats.CharsAvoided += len(value)
	return value, true
}

// Store records a result.
func (c *ResultCache) Store(key, value string) {
	if c == nil || key == "" || value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = value
}

// Invalidate drops everything the cache holds. It is called on any mutation:
// after an edit, the previous read results describe a file that no longer
// exists as read.
func (c *ResultCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return
	}
	c.entries = make(map[string]string)
	c.stats.Invalidations++
}

// Stats returns the counters.
func (c *ResultCache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Len returns how many entries the cache holds.
func (c *ResultCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// HitRate returns hits over all lookups, in [0,1].
func (s Stats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}
