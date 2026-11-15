package speed

import (
	"strings"
	"sync"
	"time"
)

// Lane is how a tool call may be executed relative to the others in a round.
type Lane int

const (
	// LaneGather is a read-only call that can run concurrently with other
	// gather calls.
	LaneGather Lane = iota
	// LaneMutation is a call that changes the workspace. It runs alone, and
	// serialises everything after it.
	LaneMutation
	// LaneBarrier is a call whose arguments depend on another call's result,
	// so it must wait for the round's earlier calls to finish.
	LaneBarrier
)

// String names the lane for logs.
func (l Lane) String() string {
	switch l {
	case LaneMutation:
		return "mutation"
	case LaneBarrier:
		return "barrier"
	default:
		return "gather"
	}
}

// Batch is the plan for one round's tool calls.
type Batch struct {
	// Planned is the ordered list of calls, each with its lane.
	Planned []PlannedCall
	// Skipped is how many calls were past the round's cap. They are not lost:
	// the caller re-emits them next round.
	Skipped int
	// Duplicates is how many were dropped as identical to an earlier call.
	Duplicates int
	// Cap is the cap that was applied, or zero when none.
	Cap int
}

// PlannedCall is one call in a batch.
type PlannedCall struct {
	// Index is the call's position in the model's output.
	Index int
	// ToolName and Key identify the call.
	ToolName string
	Key      string
	// Lane is how it may run.
	Lane Lane
}

// PoolConfig configures batch planning.
type PoolConfig struct {
	// MaxBatchCalls is how many calls one round may execute. Zero disables
	// the cap.
	MaxBatchCalls int
	// Dedupe drops a call identical to an earlier one in the same round.
	Dedupe bool
}

// DefaultPoolConfig returns the tuned defaults.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{MaxBatchCalls: 48, Dedupe: true}
}

// gatherTools are the tools whose calls may run concurrently: they only read.
var gatherTools = map[string]struct{}{
	"view": {}, "read": {}, "ls": {}, "glob": {}, "grep": {},
	"lsp_symbols": {}, "lsp_definition": {}, "lsp_references": {},
	"lsp_call_hierarchy": {}, "lsp_diagnostics": {}, "crush_info": {},
	"crush_logs": {}, "fetch": {}, "web_fetch": {}, "web_search": {},
	"sourcegraph": {}, "agentic_fetch": {}, "download": {},
	"list_mcp_resources": {}, "read_mcp_resource": {},
}

// mutationTools are the tools whose calls change the workspace.
var mutationTools = map[string]struct{}{
	"edit": {}, "multiedit": {}, "write": {}, "lsp_replace_symbol": {},
	"lsp_rename": {}, "bash": {}, "lsp_restart": {}, "job_kill": {},
}

// LaneFor classifies a tool call.
//
// A shell command is classified by what it does, not by the tool's name: the
// same `bash` call is a read when it runs `go build` and a mutation when it runs
// `sed -i`. Getting that wrong in either direction costs a round, so the
// arguments are inspected.
func LaneFor(toolName, arguments string) Lane {
	if _, ok := mutationTools[toolName]; ok {
		if toolName == "bash" {
			if !shellWrites(arguments) {
				return LaneGather
			}
			return LaneMutation
		}
		return LaneMutation
	}
	if _, ok := gatherTools[toolName]; ok {
		return LaneGather
	}
	// A tool the plan does not know is treated as a barrier: running it
	// concurrently with something that depends on it would be wrong.
	return LaneBarrier
}

// shellWrites reports whether a shell command's arguments look like a write.
func shellWrites(arguments string) bool {
	lower := strings.ToLower(arguments)
	writers := []string{
		"sed -i", "perl -pi", "tee ", " > ", ">>", "rm ", "mv ", "cp ",
		"mkdir ", "touch ", "git add", "git commit", "git checkout",
		"git restore", "go fmt", "gofmt", "npm install", "go mod tidy",
		"chmod ", "go generate",
	}
	for _, writer := range writers {
		if strings.Contains(lower, writer) {
			return true
		}
	}
	return false
}

// PlanBatch decides the order and cap for a round's calls, drops duplicates, and
// marks what has to wait.
//
// The ordering rule is simple and safe: gather calls run together, then a
// mutation runs alone, then the pattern repeats. Anything after a mutation is a
// barrier, because its arguments may describe the pre-mutation world.
func PlanBatch(calls []PlannedCall, cfg PoolConfig) Batch {
	batch := Batch{Cap: cfg.MaxBatchCalls}
	seen := make(map[string]struct{}, len(calls))
	mutationSeen := false

	for _, call := range calls {
		if cfg.Dedupe && call.Key != "" {
			if _, dup := seen[call.Key]; dup {
				batch.Duplicates++
				continue
			}
			seen[call.Key] = struct{}{}
		}
		if cfg.MaxBatchCalls > 0 && len(batch.Planned) >= cfg.MaxBatchCalls {
			batch.Skipped++
			continue
		}
		if mutationSeen {
			call.Lane = LaneBarrier
		} else if call.Lane == LaneMutation {
			mutationSeen = true
		}
		batch.Planned = append(batch.Planned, call)
	}
	return batch
}

// Prefetcher starts the read that a search result is about to require, so the
// model does not pay a full round trip to ask for it.
//
// It is a watermark, not a guess-list: a prefetched result is stored against the
// call key the model would use, and a lookup only returns it if the model asks
// for exactly that. An unused prefetch is dropped without cost.
type Prefetcher struct {
	mu sync.Mutex
	// results holds a prefetched value by cache key.
	results map[string]prefetchItem
	// dispatched and used count what the prefetcher did.
	dispatched int
	used       int
	// overlap is the total wall time a used prefetch hid.
	overlap time.Duration
	// started is when each in-flight prefetch began, by key.
	started map[string]time.Time
	// cap is how many prefetches may be outstanding.
	cap int
}

type prefetchItem struct {
	value string
	// beganAt is when the work started, so the overlap can be measured.
	beganAt time.Time
}

// DefaultPrefetchCap is how many prefetches may be outstanding at once.
const DefaultPrefetchCap = 8

// NewPrefetcher returns an empty prefetcher.
func NewPrefetcher() *Prefetcher {
	return &Prefetcher{
		results: make(map[string]prefetchItem),
		started: make(map[string]time.Time),
		cap:     DefaultPrefetchCap,
	}
}

// PrefetchStats reports what the prefetcher did in a turn.
type PrefetchStats struct {
	// Dispatched is how many prefetches were started.
	Dispatched int
	// Used is how many were actually asked for.
	Used int
	// OverlapMs is the wall time a used prefetch hid, in milliseconds.
	OverlapMs int64
}

// Begin marks a prefetch as started, so its overlap can be measured when it is
// used. It reports false when the prefetcher is full or the key is already in
// flight, which is the caller's signal to skip the work.
func (p *Prefetcher) Begin(key string, now time.Time) bool {
	if p == nil || key == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, busy := p.started[key]; busy {
		return false
	}
	if _, ready := p.results[key]; ready {
		return false
	}
	if len(p.started)+len(p.results) >= p.cap {
		return false
	}
	p.started[key] = now
	p.dispatched++
	return true
}

// Deliver records a prefetch's result. A result for a key that was never begun is
// ignored: the prefetcher never invents work.
func (p *Prefetcher) Deliver(key, value string) {
	if p == nil || key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	beganAt, ok := p.started[key]
	if !ok {
		return
	}
	// The key leaves the in-flight set either way: an empty result is not
	// worth handing back, and keeping the slot would block a real retry.
	delete(p.started, key)
	if value == "" {
		return
	}
	p.results[key] = prefetchItem{value: value, beganAt: beganAt}
}

// Take returns a prefetched result, counting the overlap it hid. The caller uses
// the value instead of running the call.
func (p *Prefetcher) Take(key string, usedAt time.Time) (string, bool) {
	if p == nil || key == "" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	item, ok := p.results[key]
	if !ok {
		return "", false
	}
	delete(p.results, key)
	p.used++
	if !item.beganAt.IsZero() {
		p.overlap += usedAt.Sub(item.beganAt)
	}
	return item.value, true
}

// Drop forgets a prefetch that was never used. It is the cheap path: an unused
// prefetch costs one map delete.
func (p *Prefetcher) Drop(key string) {
	if p == nil || key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.started, key)
	delete(p.results, key)
}

// Reset clears everything, for a new turn.
func (p *Prefetcher) Reset() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results = make(map[string]prefetchItem)
	p.started = make(map[string]time.Time)
}

// Stats returns the counters.
func (p *Prefetcher) Stats() PrefetchStats {
	if p == nil {
		return PrefetchStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return PrefetchStats{
		Dispatched: p.dispatched,
		Used:       p.used,
		OverlapMs:  p.overlap.Milliseconds(),
	}
}

// Pending returns how many prefetches are in flight or ready.
func (p *Prefetcher) Pending() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.started) + len(p.results)
}
