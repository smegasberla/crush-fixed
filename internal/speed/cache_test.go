package speed

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultCacheHitAndMiss(t *testing.T) {
	t.Parallel()

	cache := NewResultCache()
	key := CacheKey("view", `{"file_path":"a.go"}`)

	_, ok := cache.Lookup(key)
	require.False(t, ok)

	cache.Store(key, "file contents")
	value, ok := cache.Lookup(key)
	require.True(t, ok)
	require.Equal(t, "file contents", value)

	stats := cache.Stats()
	require.Equal(t, 1, stats.Hits)
	require.Equal(t, 1, stats.Misses)
	require.Equal(t, len("file contents"), stats.CharsAvoided)
	require.InDelta(t, 0.5, stats.HitRate(), 0.001)
}

func TestCacheKeyDependsOnToolAndArguments(t *testing.T) {
	t.Parallel()

	require.Equal(t, CacheKey("view", "a"), CacheKey("view", "a"))
	require.NotEqual(t, CacheKey("view", "a"), CacheKey("grep", "a"))
	require.NotEqual(t, CacheKey("view", "a"), CacheKey("view", "b"))
	require.Len(t, CacheKey("view", "a"), 32)
}

func TestResultCacheInvalidate(t *testing.T) {
	t.Parallel()

	cache := NewResultCache()
	cache.Store(CacheKey("view", "a"), "content")
	require.Equal(t, 1, cache.Len())

	cache.Invalidate()
	require.Zero(t, cache.Len())
	_, ok := cache.Lookup(CacheKey("view", "a"))
	require.False(t, ok, "a mutation invalidates every cached read")
	require.Equal(t, 1, cache.Stats().Invalidations)

	// Invalidating an empty cache is not an invalidation.
	cache.Invalidate()
	require.Equal(t, 1, cache.Stats().Invalidations)
}

func TestResultCacheNilIsSafe(t *testing.T) {
	t.Parallel()

	var cache *ResultCache
	_, ok := cache.Lookup("k")
	require.False(t, ok)
	cache.Store("k", "v")
	cache.Invalidate()
	require.Equal(t, Stats{}, cache.Stats())
	require.Zero(t, cache.Len())
}

func TestResultCacheIgnoresEmpty(t *testing.T) {
	t.Parallel()

	cache := NewResultCache()
	cache.Store("", "value")
	cache.Store("key", "")
	require.Zero(t, cache.Len())
	_, ok := cache.Lookup("")
	require.False(t, ok)
}

func TestHitRateWithoutLookups(t *testing.T) {
	t.Parallel()

	require.Zero(t, Stats{}.HitRate())
	require.Equal(t, 1.0, Stats{Hits: 3}.HitRate())
}

func TestCacheAvoidsCharsOnRepeatedLargeBodies(t *testing.T) {
	t.Parallel()

	cache := NewResultCache()
	body := strings.Repeat("x", 5000)
	key := CacheKey("bash", `{"command":"go build ./..."}`)
	cache.Store(key, body)

	_, ok := cache.Lookup(key)
	require.True(t, ok)
	require.Equal(t, 5000, cache.Stats().CharsAvoided)
}
