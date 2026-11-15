package validate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheHitAndMiss(t *testing.T) {
	t.Parallel()

	cache := NewCache()
	key := Key("go build ./...", "sig1")

	_, ok := cache.Lookup(key)
	require.False(t, ok)

	report := Report{Failures: []Failure{{Fingerprint: "a", Message: "boom"}}}
	cache.Store(key, report)

	cached, ok := cache.Lookup(key)
	require.True(t, ok)
	require.True(t, cached.Cached, "a cached report says so")
	require.Len(t, cached.Failures, 1)

	stats := cache.Stats()
	require.Equal(t, 1, stats.Hits)
	require.Equal(t, 1, stats.Misses)
	require.Equal(t, 1, stats.Runs)
	require.InDelta(t, 0.5, stats.HitRate(), 0.001)
}

func TestCacheKeyIncludesSignature(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, Key("go build", "a"), Key("go build", "b"), "a changed workspace misses")

	cache := NewCache()
	cache.Store(Key("go build", "a"), Report{})
	_, ok := cache.Lookup(Key("go build", "b"))
	require.False(t, ok)
}

func TestCacheInvalidate(t *testing.T) {
	t.Parallel()

	cache := NewCache()
	cache.Store(Key("go build", "s"), Report{})
	require.Equal(t, 1, cache.Len())

	cache.Invalidate()
	require.Zero(t, cache.Len())
	require.Equal(t, 1, cache.Stats().Invalidations)

	// Invalidating an empty cache is not counted.
	cache.Invalidate()
	require.Equal(t, 1, cache.Stats().Invalidations)
}

func TestCacheNilIsSafe(t *testing.T) {
	t.Parallel()

	var cache *Cache
	_, ok := cache.Lookup("k")
	require.False(t, ok)
	cache.Store("k", Report{})
	cache.Invalidate()
	require.Equal(t, Stats{}, cache.Stats())
	require.Zero(t, cache.Len())
	require.Zero(t, Stats{}.HitRate())
	require.Equal(t, 1.0, Stats{Hits: 2}.HitRate())
}

func TestBaseline(t *testing.T) {
	t.Parallel()

	baseline := NewBaseline()
	require.False(t, baseline.Captured())
	require.Empty(t, baseline.Known())

	baseline.Capture([]Failure{{Fingerprint: "old", File: "a.go", Line: 1}})
	require.True(t, baseline.Captured())
	require.Len(t, baseline.Known(), 1)

	preExisting, introduced := baseline.Classify([]Failure{
		{Fingerprint: "old", File: "a.go", Line: 1},
		{Fingerprint: "new", File: "b.go", Line: 2},
	})
	require.Len(t, preExisting, 1)
	require.Len(t, introduced, 1)
	require.Equal(t, "new", introduced[0].Fingerprint)
}

func TestBaselineNilIsSafe(t *testing.T) {
	t.Parallel()

	var baseline *Baseline
	baseline.Capture(nil)
	require.False(t, baseline.Captured())
	require.Nil(t, baseline.Known())

	current := []Failure{{Fingerprint: "a"}}
	preExisting, introduced := baseline.Classify(current)
	require.Nil(t, preExisting)
	require.Equal(t, current, introduced, "without a baseline everything counts as new")
}

func TestLastKnownGood(t *testing.T) {
	t.Parallel()

	ledger := NewLastKnownGood()
	require.Zero(t, ledger.GoodSequence())
	require.Contains(t, ledger.BisectHint(), "nothing changed since the last clean validation")

	require.Equal(t, 1, ledger.NoteEdit("a.go"))
	require.Contains(t, ledger.BisectHint(), "no clean validation yet in this turn")
	require.Equal(t, 2, ledger.NoteEdit("b.go"))
	require.Equal(t, []string{"a.go", "b.go"}, ledger.Drifted())

	ledger.MarkGood()
	require.Equal(t, 2, ledger.GoodSequence())
	require.Empty(t, ledger.Drifted())
	require.Contains(t, ledger.BisectHint(), "nothing changed since the last clean validation")

	ledger.NoteEdit("c.go")
	require.Equal(t, 3, ledger.Sequence())
	require.Equal(t, []string{"c.go"}, ledger.Drifted())
	require.Contains(t, ledger.BisectHint(), "c.go")
}

func TestLastKnownGoodNilIsSafe(t *testing.T) {
	t.Parallel()

	var ledger *LastKnownGood
	require.Zero(t, ledger.NoteEdit("a.go"))
	ledger.MarkGood()
	require.Zero(t, ledger.GoodSequence())
	require.Nil(t, ledger.Drifted())
	require.Zero(t, ledger.Sequence())
	require.Empty(t, ledger.BisectHint())
}

func TestBisectHintSummarisesManyFiles(t *testing.T) {
	t.Parallel()

	ledger := NewLastKnownGood()
	for _, path := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go"} {
		ledger.NoteEdit(path)
	}
	hint := ledger.BisectHint()
	require.Contains(t, hint, "a.go")
	require.Contains(t, hint, "+2 more")
}

func TestItoa(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0", itoa(0))
	require.Equal(t, "7", itoa(7))
	require.Equal(t, "42", itoa(42))
	require.Equal(t, "1000", itoa(1000))
}
