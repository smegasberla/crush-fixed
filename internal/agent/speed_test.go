package agent

import (
	"context"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/speed"
	"github.com/stretchr/testify/require"
)

func speedForTest(t *testing.T, settings *config.SpeedOptions) *speedState {
	t.Helper()
	return newSpeedState(&SpeedOptions{Settings: settings})
}

func TestNewSpeedStateDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, newSpeedState(nil))
	require.Nil(t, newSpeedState(&SpeedOptions{}))

	off := false
	require.Nil(t, newSpeedState(&SpeedOptions{
		Settings: &config.SpeedOptions{Enabled: &off},
	}))

	state := speedForTest(t, &config.SpeedOptions{})
	require.NotNil(t, state)
	require.True(t, state.enabled())
}

func TestNewTurnSpeedToggles(t *testing.T) {
	t.Parallel()

	off := false
	state := speedForTest(t, &config.SpeedOptions{
		ResultCache: &off,
		Prefetch:    &off,
	})
	turn := newTurnSpeed(state)
	require.NotNil(t, turn)
	require.Nil(t, turn.cache, "the cache is off")
	require.Nil(t, turn.prefetch, "prefetch is off")

	turn = newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	require.NotNil(t, turn.cache)
	require.NotNil(t, turn.prefetch)
}

func TestTurnSpeedNilIsSafe(t *testing.T) {
	t.Parallel()

	var turn *turnSpeed
	turn.recordConnect(time.Second)
	require.Zero(t, turn.timeout())
	require.Empty(t, turn.cacheKey("view", "{}"))
	_, ok := turn.lookup("k")
	require.False(t, ok)
	turn.store("k", "v")
	turn.invalidate()
	turn.logStats("session")

	require.Nil(t, newTurnSpeed(nil))
}

func TestTurnSpeedConnectPolicy(t *testing.T) {
	t.Parallel()

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	require.Equal(t, speed.DefaultTimeoutFloor, turn.timeout(), "no samples yet")

	for range 5 {
		turn.recordConnect(6 * time.Second)
	}
	require.Equal(t, 12*time.Second, turn.timeout(), "twice the observed p90")

	// The floor still wins for a fast endpoint. It needs its own policy: the
	// window is a percentile, so a few fast samples do not erase the slow ones.
	fast := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	for range 5 {
		fast.recordConnect(10 * time.Millisecond)
	}
	require.Equal(t, speed.DefaultTimeoutFloor, fast.timeout())
}

func TestTurnSpeedAdaptiveTimeoutDisabled(t *testing.T) {
	t.Parallel()

	off := false
	state := speedForTest(t, &config.SpeedOptions{AdaptiveTimeout: &off})
	turn := newTurnSpeed(state)
	turn.recordConnect(5 * time.Second)
	require.Zero(t, turn.timeout(), "a disabled policy imposes no timeout")
}

func TestTurnSpeedCacheRoundTrip(t *testing.T) {
	t.Parallel()

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	key := turn.cacheKey("view", `{"file_path":"a.go"}`)
	require.NotEmpty(t, key)

	_, ok := turn.lookup(key)
	require.False(t, ok)

	turn.store(key, "body")
	value, ok := turn.lookup(key)
	require.True(t, ok)
	require.Equal(t, "body", value)

	turn.invalidate()
	_, ok = turn.lookup(key)
	require.False(t, ok)
}

func TestTurnSpeedBatchPlanning(t *testing.T) {
	t.Parallel()

	calls := []speed.PlannedCall{
		{ToolName: "view", Key: "a", Lane: speed.LaneGather},
		{ToolName: "view", Key: "a", Lane: speed.LaneGather},
		{ToolName: "edit", Key: "b", Lane: speed.LaneMutation},
	}
	batch := newTurnSpeed(speedForTest(t, &config.SpeedOptions{})).planBatch(calls)
	require.Len(t, batch.Planned, 2)
	require.Equal(t, 1, batch.Duplicates)
	require.Equal(t, 48, batch.Cap)

	// Without a turn, the batch is passed through untouched.
	var turn *turnSpeed
	batch = turn.planBatch(calls)
	require.Len(t, batch.Planned, 3)
	require.Zero(t, batch.Cap)
}

func TestNewFilesTouchedByName(t *testing.T) {
	t.Parallel()

	require.True(t, newFilesTouchedByName("edit"))
	require.True(t, newFilesTouchedByName("write"))
	require.True(t, newFilesTouchedByName("bash"))
	require.False(t, newFilesTouchedByName("view"))
	require.False(t, newFilesTouchedByName("grep"))
}

func TestCacheableToolName(t *testing.T) {
	t.Parallel()

	require.True(t, cacheableToolName("view"))
	require.True(t, cacheableToolName("grep"))
	require.True(t, cacheableToolName("lsp_symbols"))
	require.False(t, cacheableToolName("edit"))
	require.False(t, cacheableToolName("bash"))
	require.False(t, cacheableToolName("write"))
}

// stubTool is a minimal fantasy.AgentTool for exercising the cache decorator.
type stubTool struct {
	name  string
	runs  []string
	body  string
	isErr bool
}

func (s *stubTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: s.name, Parameters: map[string]any{}}
}

func (s *stubTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (s *stubTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (s *stubTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	s.runs = append(s.runs, call.Input)
	if s.isErr {
		return fantasy.NewTextErrorResponse("boom"), nil
	}
	return fantasy.NewTextResponse(s.body), nil
}

func TestCachedToolServesRepeatedRead(t *testing.T) {
	t.Parallel()

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	inner := &stubTool{name: "view", body: "file body"}
	wrapped := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, cacheable: true}

	call := fantasy.ToolCall{Name: "view", Input: `{"file_path":"a.go"}`}
	first, err := wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, "file body", first.Content)

	second, err := wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	require.Equal(t, "file body", second.Content)
	require.Len(t, inner.runs, 1, "the second call was answered from the cache")
	require.Equal(t, 1, turn.cache.Stats().Hits)
}

func TestCachedToolBypassesUncacheableTools(t *testing.T) {
	t.Parallel()

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	inner := &stubTool{name: "bash", body: "output"}
	wrapped := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, cacheable: false}

	call := fantasy.ToolCall{Name: "bash", Input: `{"command":"ls"}`}
	_, err := wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	_, err = wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, inner.runs, 2, "a shell command is never served from cache")
}

func TestCachedToolDoesNotCacheErrors(t *testing.T) {
	t.Parallel()

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	inner := &stubTool{name: "view", body: "boom", isErr: true}
	wrapped := &cachedTool{inner: inner, turn: func() *turnSpeed { return turn }, cacheable: true}

	call := fantasy.ToolCall{Name: "view", Input: `{"file_path":"a.go"}`}
	_, err := wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	_, err = wrapped.Run(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, inner.runs, 2, "an error is not a stable result")
	require.Zero(t, turn.cache.Len())
}

func TestWrapToolsWithResultCache(t *testing.T) {
	t.Parallel()

	tools := []fantasy.AgentTool{&stubTool{name: "view"}, &stubTool{name: "edit"}}
	require.Equal(t, tools, wrapToolsWithResultCache(tools, nil), "the layer off leaves the list alone")

	turn := newTurnSpeed(speedForTest(t, &config.SpeedOptions{}))
	wrapped := wrapToolsWithResultCache(tools, turn)
	require.Len(t, wrapped, 2)
	for _, tool := range wrapped {
		_, ok := tool.(*cachedTool)
		require.True(t, ok)
	}
}

func TestCachedToolDelegatesInfo(t *testing.T) {
	t.Parallel()

	inner := &stubTool{name: "view"}
	wrapped := &cachedTool{inner: inner, cacheable: true}
	require.Equal(t, "view", wrapped.Info().Name)
	require.Nil(t, wrapped.ProviderOptions())
	wrapped.SetProviderOptions(nil)
}

func TestCacheReadTokens(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(0), cacheReadTokens(fantasy.Usage{}))
	require.Equal(t, int64(42), cacheReadTokens(fantasy.Usage{CacheReadTokens: 42}))
}
