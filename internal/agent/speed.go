package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/governor"
	"github.com/charmbracelet/crush/internal/speed"
	"github.com/charmbracelet/crush/internal/validate"
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
	// rechecks counts how many needless re-verifications were answered from
	// the previous result instead of being run again.
	rechecks int
	// guardRechecks is whether a repeated verification may be refused.
	guardRechecks bool
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
	// The recheck guard rides on the cache, so it needs one to exist.
	turn.guardRechecks = state.settings.GetRecheckGuard()
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
	if t.rechecks > 0 {
		slog.Info("Refused a needless recheck",
			"session_id", sessionID, "count", t.rechecks,
			"detail", "an identical command already produced this answer on the same workspace")
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

// cacheReadTokens returns how many of a round's input tokens the provider served
// from its prefix cache. It is logged so a prefix-cache regression is visible: a
// prompt that changes near the front shows up here as zero, not as a mystery
// slowdown.
func cacheReadTokens(usage fantasy.Usage) int64 { return usage.CacheReadTokens }

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
	// recheck reports whether an identical re-run may be answered from the
	// previous result. It applies only to verification commands, whose answer
	// is a function of the workspace the cache is invalidated on.
	recheck bool
}

// wrapToolsWithResultCache returns tools wrapped so a repeated read is answered
// from the turn's cache and a repeated verification is not re-run. It returns
// the slice unchanged when the layer is off.
func wrapToolsWithResultCache(tools []fantasy.AgentTool, turn *turnSpeed) []fantasy.AgentTool {
	if turn == nil || turn.cache == nil {
		return tools
	}
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		name := tool.Info().Name
		out[i] = &cachedTool{
			inner:     tool,
			turn:      func() *turnSpeed { return turn },
			cacheable: cacheableToolName(name),
			recheck:   speed.Recheckable(name),
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
	if turn == nil || (!c.cacheable && !c.recheck) {
		return c.inner.Run(ctx, call)
	}
	// A re-run is only refused when it provably cannot differ: the tool is a
	// verification, the call is a validation command, and it does not write.
	// Anything else runs normally, because a second run of a command that
	// produces new output is not a recheck.
	guarded := turn.guardRechecks && c.recheck && !c.cacheable && callRecheckEligible(call)
	if !c.cacheable && !guarded {
		// Neither cacheable nor a reproducible check: run it, and do not
		// remember the answer. A shell command that lists files or writes is
		// exactly this case.
		return c.inner.Run(ctx, call)
	}
	key := turn.cacheKey(call.Name, call.Input)
	if key == "" {
		return c.inner.Run(ctx, call)
	}
	if cached, ok := turn.lookup(key); ok {
		if guarded {
			turn.noteRecheck()
			slog.Debug("Verification not re-run", "tool", call.Name)
			return fantasy.NewTextResponse(notReRunNote(call.Name, cached)), nil
		}
		slog.Debug("Tool result served from cache", "tool", call.Name, "chars", len(cached))
		return fantasy.NewTextResponse(cached), nil
	}
	response, err := c.inner.Run(ctx, call)
	if err != nil || response.IsError || response.Content == "" {
		return response, err
	}
	// A failed verification is not stored: re-running it is how a fix is
	// confirmed, and a cached failure would hide the fix.
	if guarded && responseIsFailure(response.Content) {
		return response, err
	}
	turn.store(key, response.Content)
	if guarded {
		return fantasy.WithResponseMetadata(
			fantasy.NewTextResponse(response.Content+"\n\n"+firstRunNote()),
			response.Metadata,
		), err
	}
	return response, err
}

// callRecheckEligible reports whether this specific call may be answered from
// its previous result.
//
// For a shell command that means it is a build, test or type check and not a
// write: `go test` cannot produce a different answer on an unchanged workspace,
// while `git status` can, because the user may have changed something outside.
func callRecheckEligible(call fantasy.ToolCall) bool {
	switch call.Name {
	case "bash":
		return validate.IsValidationCommand(inputCommand(call.Input)) && !speed.ShellWrites(call.Input)
	default:
		return !speed.ShellWrites(call.Input)
	}
}

// inputCommand extracts a shell command from a tool call's arguments.
func inputCommand(input string) string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return input
	}
	return payload.Command
}

// noteRecheck records one refused re-verification.
func (t *turnSpeed) noteRecheck() {
	if t == nil {
		return
	}
	t.rechecks++
}

// firstRunNote is appended to a verification result the first time it is
// produced, so the model knows the answer is now known for this workspace.
func firstRunNote() string {
	return "[This is the result for the current workspace. It does not need to be run again unless a file changes.]"
}

// notReRunNote is the whole answer for a repeated verification.
func notReRunNote(toolName, previous string) string {
	return "[Not run again: " + toolName + " already produced this result on the current workspace, and nothing has changed since. " +
		"Re-running it would return the same thing.\n\n" + previous + "]"
}

// responseIsFailure reports whether a verification output says the check failed.
// It is deliberately conservative: anything that names a failure, an error, or a
// non-zero exit counts, so a failure is never cached.
func responseIsFailure(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range []string{
		"build failed", "failed", "failure", "error", "exit status 1", "exit code 1",
		"panic:", "--- fail", "fatal", "not ok",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
