package validate

import (
	"sync"
	"time"
)

// Cache answers a repeated check from the result the first one produced, so a
// turn that runs the same build three times pays for it once.
//
// An entry is keyed by the command, and invalidated by a mutation: a build's
// result is only meaningful until the source changes. The cache also remembers
// which failures were already reported, which is what stops a round from
// re-reporting a failure the previous round already knew about.
type Cache struct {
	mu sync.Mutex
	// entries is the result of the last run of each command.
	entries map[string]entry
	// stats are the counters a caller reports.
	stats Stats
}

type entry struct {
	report Report
	// at is when the run happened.
	at time.Time
	// signature is the workspace signature the result was produced from.
	signature string
}

// Stats reports what the cache did.
type Stats struct {
	// Hits is how many checks were answered without running.
	Hits int
	// Misses is how many had to run.
	Misses int
	// Invalidations is how many times a mutation cleared the cache.
	Invalidations int
	// Runs is how many checks actually executed.
	Runs int
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]entry)}
}

// Key builds the cache key for a check: the command plus the workspace signature
// it was run against.
func Key(command, signature string) string {
	return command + "\x00" + signature
}

// Lookup returns a cached report for a check.
func (c *Cache) Lookup(key string) (Report, bool) {
	if c == nil || key == "" {
		return Report{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	found, ok := c.entries[key]
	if !ok {
		c.stats.Misses++
		return Report{}, false
	}
	c.stats.Hits++
	report := found.report
	report.Cached = true
	return report, true
}

// Store records a check's result.
func (c *Cache) Store(key string, report Report) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Runs++
	report.Cached = false
	c.entries[key] = entry{report: report, at: time.Now()}
}

// Invalidate drops everything: after a mutation, no cached result describes the
// workspace any more.
func (c *Cache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return
	}
	c.entries = make(map[string]entry)
	c.stats.Invalidations++
}

// Stats returns the counters.
func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Len returns how many results the cache holds.
func (c *Cache) Len() int {
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

// Baseline is the set of failures a turn started from. It is what separates a
// regression from a pre-existing problem: a failing build that was already
// failing is not this turn's fault, and chasing it wastes the turn.
type Baseline struct {
	mu sync.Mutex
	// known is the fingerprint set captured at the start of the turn.
	known map[string]Failure
	// captured is false until the first baseline is recorded.
	captured bool
}

// NewBaseline returns an empty baseline.
func NewBaseline() *Baseline {
	return &Baseline{known: make(map[string]Failure)}
}

// Capture records the failures present before the turn's first change.
func (b *Baseline) Capture(failures []Failure) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, failure := range failures {
		b.known[failure.Fingerprint] = failure
	}
	b.captured = true
}

// Captured reports whether a baseline exists.
func (b *Baseline) Captured() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.captured
}

// Known returns the captured failures.
func (b *Baseline) Known() []Failure {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Failure, 0, len(b.known))
	for _, failure := range b.known {
		out = append(out, failure)
	}
	SortFailures(out)
	return out
}

// Classify splits a run's failures into pre-existing and introduced, using the
// captured baseline.
func (b *Baseline) Classify(current []Failure) (preExisting, introduced []Failure) {
	if b == nil {
		return nil, current
	}
	return PreExistingFromBaseline(b.Known(), current)
}

// LastKnownGood remembers the last change set that validated cleanly, so a
// failure introduced later can be bisected rather than guessed at.
type LastKnownGood struct {
	mu sync.Mutex
	// sequence is the change counter the turn numbers its edits with.
	sequence int
	// goodSequence is the counter value of the last clean validation.
	goodSequence int
	// goodFiles is the set of files that were edited up to that point.
	goodFiles []string
	// drifted is the files touched since the last good state.
	drifted []string
}

// NewLastKnownGood returns an empty ledger.
func NewLastKnownGood() *LastKnownGood {
	return &LastKnownGood{}
}

// NoteEdit records one changed file and advances the counter.
func (l *LastKnownGood) NoteEdit(path string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sequence++
	l.drifted = append(l.drifted, path)
	return l.sequence
}

// MarkGood records that the workspace validated cleanly at the current counter.
func (l *LastKnownGood) MarkGood() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.goodSequence = l.sequence
	l.goodFiles = append([]string(nil), l.drifted...)
	l.drifted = nil
}

// GoodSequence returns the counter of the last clean validation, or zero.
func (l *LastKnownGood) GoodSequence() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.goodSequence
}

// Drifted returns the files edited since the last clean validation.
func (l *LastKnownGood) Drifted() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.drifted...)
}

// Sequence returns the current change counter.
func (l *LastKnownGood) Sequence() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sequence
}

// BisectHint describes where to look first when a check fails. It names the
// files changed since the last clean state, which is the smallest set that can
// contain the cause.
func (l *LastKnownGood) BisectHint() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.drifted) == 0 {
		return "nothing changed since the last clean validation, so the failure is not from this turn"
	}
	// Naming the files is the useful part; whether a clean validation ever
	// happened only changes how the hint is phrased.
	if l.goodSequence == 0 {
		return "no clean validation yet in this turn; changed so far: " + joinLimited(l.drifted, 6)
	}
	return "changed since the last clean validation: " + joinLimited(l.drifted, 6)
}

// joinLimited joins up to limit items and summarises the rest.
func joinLimited(items []string, limit int) string {
	if len(items) <= limit {
		return joinStrings(items, ", ")
	}
	return joinStrings(items[:limit], ", ") + " (+" + itoa(len(items)-limit) + " more)"
}

func joinStrings(items []string, separator string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += separator
		}
		out += item
	}
	return out
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
