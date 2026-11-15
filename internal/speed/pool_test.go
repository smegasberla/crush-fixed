package speed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLaneForGatherTools(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"view", "grep", "glob", "ls", "web_search", "sourcegraph", "fetch"} {
		require.Equal(t, LaneGather, LaneFor(name, "{}"), "%s should gather", name)
	}
}

func TestLaneForMutationTools(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"edit", "multiedit", "write", "lsp_replace_symbol", "lsp_rename"} {
		require.Equal(t, LaneMutation, LaneFor(name, "{}"), "%s should mutate", name)
	}
}

func TestLaneForBashFollowsTheCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, LaneGather, LaneFor("bash", `{"command":"go build ./..."}`))
	require.Equal(t, LaneGather, LaneFor("bash", `{"command":"go test ./internal/agent/"}`))
	require.Equal(t, LaneMutation, LaneFor("bash", `{"command":"sed -i 's/a/b/' f.go"}`))
	require.Equal(t, LaneMutation, LaneFor("bash", `{"command":"go mod tidy"}`))
	require.Equal(t, LaneMutation, LaneFor("bash", `{"command":"rm -rf build"}`))
}

func TestLaneForUnknownToolIsBarrier(t *testing.T) {
	t.Parallel()

	require.Equal(t, LaneBarrier, LaneFor("mystery_tool", "{}"))
}

func TestLaneString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "gather", LaneGather.String())
	require.Equal(t, "mutation", LaneMutation.String())
	require.Equal(t, "barrier", LaneBarrier.String())
}

func TestPlanBatchKeepsOrderAndLanes(t *testing.T) {
	t.Parallel()

	calls := []PlannedCall{
		{Index: 0, ToolName: "view", Key: "a", Lane: LaneGather},
		{Index: 1, ToolName: "grep", Key: "b", Lane: LaneGather},
	}
	batch := PlanBatch(calls, DefaultPoolConfig())
	require.Len(t, batch.Planned, 2)
	require.Equal(t, LaneGather, batch.Planned[0].Lane)
	require.Equal(t, LaneGather, batch.Planned[1].Lane)
	require.Zero(t, batch.Skipped)
}

func TestPlanBatchSerialisesAfterMutation(t *testing.T) {
	t.Parallel()

	calls := []PlannedCall{
		{Index: 0, ToolName: "view", Key: "a", Lane: LaneGather},
		{Index: 1, ToolName: "edit", Key: "b", Lane: LaneMutation},
		{Index: 2, ToolName: "view", Key: "c", Lane: LaneGather},
	}
	batch := PlanBatch(calls, DefaultPoolConfig())
	require.Len(t, batch.Planned, 3)
	require.Equal(t, LaneGather, batch.Planned[0].Lane)
	require.Equal(t, LaneMutation, batch.Planned[1].Lane)
	require.Equal(t, LaneBarrier, batch.Planned[2].Lane, "anything after a mutation must wait for it")
}

func TestPlanBatchDedupes(t *testing.T) {
	t.Parallel()

	calls := []PlannedCall{
		{Index: 0, ToolName: "view", Key: "same", Lane: LaneGather},
		{Index: 1, ToolName: "view", Key: "same", Lane: LaneGather},
		{Index: 2, ToolName: "view", Key: "other", Lane: LaneGather},
	}
	batch := PlanBatch(calls, PoolConfig{Dedupe: true, MaxBatchCalls: 10})
	require.Len(t, batch.Planned, 2)
	require.Equal(t, 1, batch.Duplicates)
	require.Equal(t, "other", batch.Planned[1].Key)
}

func TestPlanBatchDedupeDisabled(t *testing.T) {
	t.Parallel()

	calls := []PlannedCall{
		{Index: 0, ToolName: "view", Key: "same", Lane: LaneGather},
		{Index: 1, ToolName: "view", Key: "same", Lane: LaneGather},
	}
	batch := PlanBatch(calls, PoolConfig{Dedupe: false, MaxBatchCalls: 10})
	require.Len(t, batch.Planned, 2)
	require.Zero(t, batch.Duplicates)
}

func TestPlanBatchCapSkipsRatherThanTruncates(t *testing.T) {
	t.Parallel()

	calls := make([]PlannedCall, 0, 20)
	for i := range 20 {
		calls = append(calls, PlannedCall{Index: i, ToolName: "view", Key: string(rune('a' + i)), Lane: LaneGather})
	}
	batch := PlanBatch(calls, PoolConfig{MaxBatchCalls: 8, Dedupe: true})
	require.Len(t, batch.Planned, 8)
	require.Equal(t, 12, batch.Skipped, "calls past the cap are reported, not silently dropped")
	require.Equal(t, 8, batch.Cap)
}

func TestPlanBatchNoCap(t *testing.T) {
	t.Parallel()

	calls := make([]PlannedCall, 20)
	for i := range calls {
		calls[i] = PlannedCall{Index: i, ToolName: "view", Key: string(rune('a' + i)), Lane: LaneGather}
	}
	batch := PlanBatch(calls, PoolConfig{MaxBatchCalls: 0})
	require.Len(t, batch.Planned, 20)
	require.Zero(t, batch.Skipped)
	require.Zero(t, batch.Cap)
}

func TestPrefetcherLifecycle(t *testing.T) {
	t.Parallel()

	prefetcher := NewPrefetcher()
	start := time.Unix(1000, 0)

	require.True(t, prefetcher.Begin("k", start))
	require.False(t, prefetcher.Begin("k", start), "a key in flight is not restarted")
	require.Equal(t, 1, prefetcher.Pending())

	prefetcher.Deliver("k", "file body")
	value, ok := prefetcher.Take("k", start.Add(250*time.Millisecond))
	require.True(t, ok)
	require.Equal(t, "file body", value)
	require.Zero(t, prefetcher.Pending())

	stats := prefetcher.Stats()
	require.Equal(t, 1, stats.Dispatched)
	require.Equal(t, 1, stats.Used)
	require.Equal(t, int64(250), stats.OverlapMs)
}

func TestPrefetcherDropsUnused(t *testing.T) {
	t.Parallel()

	prefetcher := NewPrefetcher()
	start := time.Unix(1000, 0)
	prefetcher.Begin("k", start)
	prefetcher.Deliver("k", "value")
	prefetcher.Drop("k")
	require.Zero(t, prefetcher.Pending())

	_, ok := prefetcher.Take("k", start)
	require.False(t, ok)
	require.Zero(t, prefetcher.Stats().Used)
}

func TestPrefetcherCap(t *testing.T) {
	t.Parallel()

	prefetcher := NewPrefetcher()
	prefetcher.cap = 2
	start := time.Unix(1000, 0)
	require.True(t, prefetcher.Begin("a", start))
	require.True(t, prefetcher.Begin("b", start))
	require.False(t, prefetcher.Begin("c", start), "the prefetcher is full")
}

func TestPrefetcherIgnoresInventedWork(t *testing.T) {
	t.Parallel()

	prefetcher := NewPrefetcher()
	prefetcher.Deliver("never-started", "value")
	require.Zero(t, prefetcher.Pending())

	prefetcher.Begin("k", time.Unix(0, 0))
	prefetcher.Deliver("k", "")
	require.Zero(t, prefetcher.Pending(), "an empty result is not delivered")
}

func TestPrefetcherNilIsSafe(t *testing.T) {
	t.Parallel()

	var prefetcher *Prefetcher
	require.False(t, prefetcher.Begin("k", time.Time{}))
	prefetcher.Deliver("k", "v")
	prefetcher.Drop("k")
	prefetcher.Reset()
	_, ok := prefetcher.Take("k", time.Time{})
	require.False(t, ok)
	require.Equal(t, PrefetchStats{}, prefetcher.Stats())
	require.Zero(t, prefetcher.Pending())
}

func TestPrefetcherReset(t *testing.T) {
	t.Parallel()

	prefetcher := NewPrefetcher()
	start := time.Unix(1000, 0)
	prefetcher.Begin("k", start)
	prefetcher.Deliver("k", "v")
	prefetcher.Reset()
	require.Zero(t, prefetcher.Pending())
	_, ok := prefetcher.Take("k", start)
	require.False(t, ok)
}

func TestDefaultPoolConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultPoolConfig()
	require.Equal(t, 48, cfg.MaxBatchCalls)
	require.True(t, cfg.Dedupe)
}
