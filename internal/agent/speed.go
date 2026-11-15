package agent

import (
	"context"
	"log/slog"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/governor"
	"github.com/charmbracelet/crush/internal/speed"
)

// SpeedOptions carries the request-latency settings. Nil disables the layer.
type SpeedOptions struct {
	Settings *config.SpeedOptions
}

// speedState is the per-agent runtime for the latency layer: one connect policy
// (shared across turns, because it learns about the endpoint, not the turn) and
// the per-turn cache and prefetcher.
type speedState struct {
	settings *config.SpeedOptions
	connects *speed.ConnectPolicy
	endpoint string
}

// newSpeedState builds the runtime, or nil when the layer is disabled.
func newSpeedState(opts *SpeedOptions) *speedState {
	if opts == nil || opts.Settings == nil {
		return nil
	}
	if !opts.Settings.SpeedEnabled() {
		return nil
	}
	return &speedState{
		settings: opts.Settings,
		connects: speed.NewConnectPolicy(),
	}
}

// enabled reports whether the layer should run.
func (s *speedState) enabled() bool { return s != nil && s.settings != nil }

// turnSpeed is one turn's latency state: the result cache and the prefetcher.
type turnSpeed struct {
	state    *speedState
	cache    *speed.ResultCache
	prefetch *speed.Prefetcher
	// planCap is the batch cap for the turn.
	planCap int
	dedupe  bool
}

// newTurnSpeed builds the turn's state, or nil when the layer is off.
func newTurnSpeed(state *speedState) *turnSpeed {
	if !state.enabled() {
		return nil
	}
	turn := &turnSpeed{
		state:   state,
		planCap: state.settings.GetMaxBatchCalls(),
		dedupe:  state.settings.GetDedupeCalls(),
	}
	if state.settings.GetResultCache() {
		turn.cache = speed.NewResultCache()
	}
	if state.settings.GetPrefetch() {
		turn.prefetch = speed.NewPrefetcher()
	}
	return turn
}

// endpointKey is the identity the connect policy learns under. An agent without
// a configured endpoint still learns, under one shared key: the samples are still
// about the provider this process talks to.
func (t *turnSpeed) endpointKey() string {
	if t == nil {
		return ""
	}
	if t.state.endpoint != "" {
		return t.state.endpoint
	}
	return defaultEndpointKey
}

// defaultEndpointKey is the policy key used when no endpoint was configured.
const defaultEndpointKey = "default"

// recordConnect records one observed connect latency, so the timeout for this
// endpoint is sized from evidence.
func (t *turnSpeed) recordConnect(latency time.Duration) {
	if t == nil || !t.state.settings.GetAdaptiveTimeout() {
		return
	}
	t.state.connects.Record(t.endpointKey(), latency)
}

// timeout returns the timeout to use for this endpoint.
func (t *turnSpeed) timeout() time.Duration {
	if t == nil || !t.state.settings.GetAdaptiveTimeout() {
		return 0
	}
	return t.state.connects.Timeout(t.endpointKey())
}

// cacheKey builds the key for a tool call, or "" when caching is off.
func (t *turnSpeed) cacheKey(toolName, arguments string) string {
	if t == nil || t.cache == nil {
		return ""
	}
	return speed.CacheKey(toolName, arguments)
}

// lookup answers a repeated call from the cache.
func (t *turnSpeed) lookup(key string) (string, bool) {
	if t == nil || t.cache == nil || key == "" {
		return "", false
	}
	return t.cache.Lookup(key)
}

// store records a tool result for a possible repeat.
func (t *turnSpeed) store(key, value string) {
	if t == nil || t.cache == nil {
		return
	}
	t.cache.Store(key, value)
}

// invalidate clears the cache after a mutation: a read result taken before the
// change no longer describes the workspace.
func (t *turnSpeed) invalidate() {
	if t == nil || t.cache == nil {
		return
	}
	t.cache.Invalidate()
}

// planBatch decides the order and cap for a round's calls.
func (t *turnSpeed) planBatch(calls []speed.PlannedCall) speed.Batch {
	if t == nil {
		return speed.Batch{Planned: calls}
	}
	return speed.PlanBatch(calls, speed.PoolConfig{MaxBatchCalls: t.planCap, Dedupe: t.dedupe})
}

// logStats emits the turn's latency counters, so the layer is measurable rather
// than assumed.
func (t *turnSpeed) logStats(sessionID string) {
	if t == nil {
		return
	}
	cacheStats := speed.Stats{}
	if t.cache != nil {
		cacheStats = t.cache.Stats()
	}
	prefetchStats := speed.PrefetchStats{}
	if t.prefetch != nil {
		prefetchStats = t.prefetch.Stats()
	}
	if cacheStats.Hits == 0 && cacheStats.Misses == 0 && prefetchStats.Dispatched == 0 {
		return
	}
	slog.Info("Turn latency counters",
		"session_id", sessionID,
		"cache_hits", cacheStats.Hits,
		"cache_misses", cacheStats.Misses,
		"cache_hit_rate", cacheStats.HitRate(),
		"chars_avoided", cacheStats.CharsAvoided,
		"cache_invalidations", cacheStats.Invalidations,
		"prefetch_dispatched", prefetchStats.Dispatched,
		"prefetch_used", prefetchStats.Used,
		"prefetch_overlap_ms", prefetchStats.OverlapMs,
		"connect_p90_ms", t.state.connects.P90(t.endpointKey()).Milliseconds(),
		"connect_samples", t.state.connects.Samples(t.endpointKey()))
}

// speedConfigFor builds the speed options from the loaded config.
func (c *coordinator) speedConfigFor() *SpeedOptions {
	settings := c.cfg.Config().Options.GetSpeed()
	if !settings.SpeedEnabled() {
		return nil
	}
	return &SpeedOptions{Settings: settings}
}

// speedFor returns the turn's latency state, or nil when the layer is off.
func (a *sessionAgent) speedFor() *turnSpeed {
	return newTurnSpeed(a.speed)
}

// newFilesTouchedByName reports whether a completed tool mutates the workspace.
// It is the tool-level counterpart of newFilesTouched, used where only the result
// is available.
func newFilesTouchedByName(toolName string) bool {
	switch actionKindForTool(toolName) {
	case governor.ActionEdit:
		return true
	}
	return toolName == "bash"
}

// cachedTool answers a repeated tool call from the turn's result cache instead of
// running it again.
//
// Only calls the cache is allowed to answer are cached: a read whose result
// cannot change between two identical calls. Mutations are never cached, and the
// cache is cleared on every mutation, so a cached read can only ever describe the
// workspace as it is now.
type cachedTool struct {
	inner fantasy.AgentTool
	turn  func() *turnSpeed
	// cacheable reports whether this tool's calls may be cached.
	cacheable bool
}

// wrapToolsWithResultCache returns tools wrapped so a repeated read is answered
// from the turn's cache. It returns the slice unchanged when the layer is off.
func wrapToolsWithResultCache(tools []fantasy.AgentTool, turn *turnSpeed) []fantasy.AgentTool {
	if turn == nil || turn.cache == nil {
		return tools
	}
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = &cachedTool{
			inner:     tool,
			turn:      func() *turnSpeed { return turn },
			cacheable: cacheableToolName(tool.Info().Name),
		}
	}
	return out
}

// cacheableToolName reports whether a tool's results may be cached. Only tools
// that read are listed: anything absent is treated as uncacheable, which is the
// safe default.
func cacheableToolName(name string) bool {
	switch name {
	case "view", "ls", "glob", "grep", "crush_info", "lsp_symbols",
		"lsp_definition", "lsp_references", "lsp_call_hierarchy", "lsp_diagnostics":
		return true
	default:
		return false
	}
}

func (c *cachedTool) Info() fantasy.ToolInfo { return c.inner.Info() }

func (c *cachedTool) ProviderOptions() fantasy.ProviderOptions { return c.inner.ProviderOptions() }

func (c *cachedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	c.inner.SetProviderOptions(opts)
}

// Run serves a repeated read from the cache, or runs the tool and stores the
// result for a possible repeat.
func (c *cachedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	turn := c.turn()
	if !c.cacheable || turn == nil {
		return c.inner.Run(ctx, call)
	}
	key := turn.cacheKey(call.Name, call.Input)
	if key == "" {
		return c.inner.Run(ctx, call)
	}
	if cached, ok := turn.lookup(key); ok {
		slog.Debug("Tool result served from cache", "tool", call.Name, "chars", len(cached))
		return fantasy.NewTextResponse(cached), nil
	}
	response, err := c.inner.Run(ctx, call)
	if err != nil || response.IsError || response.Content == "" {
		return response, err
	}
	turn.store(key, response.Content)
	return response, err
}
