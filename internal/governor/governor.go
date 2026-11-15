// Package governor keeps an agent turn from grinding: it scores how much of
// the turn was spent exploring without learning anything, escalates steering
// notes as that score rises, and refuses only the one repetition that provably
// cannot help.
//
// The model is a pressure number in [0,1] computed from a bounded history of
// observed actions, not a timer and not a round cap. A productive turn never
// reaches the first threshold, which is the property that makes the steering
// tolerable: it speaks only when the work has stopped paying for itself.
package governor

import (
	"math"
	"strings"
	"sync"
	"time"
)

// Level is how hard the governor is currently steering.
type Level int

const (
	// LevelNone means the turn is within normal bounds.
	LevelNone Level = iota
	// LevelSoft is the first steering note.
	LevelSoft
	// LevelModerate adds the "why are you searching" steer.
	LevelModerate
	// LevelStrong escalates while the behaviour persists.
	LevelStrong
	// LevelHard is the only refusal, and only for an already-answered repeat.
	LevelHard
)

// String returns the level's name, as used in logs and notes.
func (l Level) String() string {
	switch l {
	case LevelSoft:
		return "soft"
	case LevelModerate:
		return "moderate"
	case LevelStrong:
		return "strong"
	case LevelHard:
		return "hard"
	default:
		return "none"
	}
}

// Config holds the pressure thresholds. The defaults mirror the tuned values:
// a quarter of the way to stagnation is worth a note, most of the way is worth
// a firm one, and only the last fifth can refuse anything.
type Config struct {
	// SoftThreshold, ModerateThreshold, StrongThreshold and HardThreshold are
	// the pressure values at which each level starts.
	SoftThreshold     float64
	ModerateThreshold float64
	StrongThreshold   float64
	HardThreshold     float64
	// RepeatedSearchThreshold is how many identical searches count as
	// repetition.
	RepeatedSearchThreshold int
	// RepeatedReadThreshold is how many reads of one region count as
	// repetition.
	RepeatedReadThreshold int
	// NoProgressThreshold is how many failed or empty results in a row push
	// the turn into stagnation.
	NoProgressThreshold int
	// TimeWarning and TimeCritical are elapsed-time multipliers. They multiply
	// pressure; they are never a deadline that ends a turn.
	TimeWarning  time.Duration
	TimeCritical time.Duration
	// HistorySize bounds the action records the score is computed from.
	HistorySize int
	// Enabled turns the whole governor off.
	Disabled bool
}

// DefaultConfig returns the tuned defaults.
func DefaultConfig() Config {
	return Config{
		SoftThreshold:           0.25,
		ModerateThreshold:       0.45,
		StrongThreshold:         0.65,
		HardThreshold:           0.85,
		RepeatedSearchThreshold: 3,
		RepeatedReadThreshold:   3,
		NoProgressThreshold:     5,
		TimeWarning:             240 * time.Second,
		TimeCritical:            600 * time.Second,
		HistorySize:             40,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.SoftThreshold <= 0 {
		c.SoftThreshold = d.SoftThreshold
	}
	if c.ModerateThreshold <= 0 {
		c.ModerateThreshold = d.ModerateThreshold
	}
	if c.StrongThreshold <= 0 {
		c.StrongThreshold = d.StrongThreshold
	}
	if c.HardThreshold <= 0 {
		c.HardThreshold = d.HardThreshold
	}
	if c.RepeatedSearchThreshold <= 0 {
		c.RepeatedSearchThreshold = d.RepeatedSearchThreshold
	}
	if c.RepeatedReadThreshold <= 0 {
		c.RepeatedReadThreshold = d.RepeatedReadThreshold
	}
	if c.NoProgressThreshold <= 0 {
		c.NoProgressThreshold = d.NoProgressThreshold
	}
	if c.TimeWarning <= 0 {
		c.TimeWarning = d.TimeWarning
	}
	if c.TimeCritical <= 0 {
		c.TimeCritical = d.TimeCritical
	}
	if c.HistorySize <= 0 {
		c.HistorySize = d.HistorySize
	}
	return c
}

// ActionKind classifies what a turn did. Only the distinction that matters for
// pressure is modelled: did the turn gather evidence, change something, or
// learn nothing at all?
type ActionKind int

const (
	// ActionRead is a read-only inspection of a file or region.
	ActionRead ActionKind = iota
	// ActionSearch is a search or glob.
	ActionSearch
	// ActionEdit is a mutation of the workspace.
	ActionEdit
	// ActionRun is a command that produced evidence (build, test, check).
	ActionRun
	// ActionEmpty is an action that failed or returned nothing.
	ActionEmpty
	// ActionOther is anything else.
	ActionOther
)

// String names the action kind for logs.
func (k ActionKind) String() string {
	switch k {
	case ActionRead:
		return "read"
	case ActionSearch:
		return "search"
	case ActionEdit:
		return "edit"
	case ActionRun:
		return "run"
	case ActionEmpty:
		return "empty"
	default:
		return "other"
	}
}

// Action is one observed tool execution.
type Action struct {
	// Kind is what the action was.
	Kind ActionKind
	// Fingerprint identifies the action's arguments, so repeating it can be
	// detected without storing the arguments themselves.
	Fingerprint string
	// Empty reports that the action produced no usable output.
	Empty bool
	// At is when it happened.
	At time.Time
}

// Report is the governor's assessment after an action.
type Report struct {
	// Level is how hard to steer.
	Level Level
	// Pressure is the score in [0,1].
	Pressure float64
	// Reason is a short clause for logs, such as "no new information for 3
	// actions".
	Reason string
	// Progress is the share of recent actions that changed something.
	Progress float64
	// RepeatedSearches and RepeatedReads count identical repeats in the
	// history.
	RepeatedSearches int
	RepeatedReads    int
	// NoProgressActions counts consecutive empty results.
	NoProgressActions int
	// Searches, Reads, Edits and Runs are the totals for the turn.
	Searches int
	Reads    int
	Edits    int
	Runs     int
	// Elapsed is how long the turn has been running.
	Elapsed time.Duration
	// Readiness is the 0..1 confidence that enough context was gathered to
	// act: it rises with each distinct file read and each edit, and falls
	// with every empty result.
	Readiness float64
	// Edited is true once the turn changed a file.
	Edited bool
}

// Governor scores one turn's exploration.
//
// It is safe for concurrent use because tool results arrive concurrently for
// parallel calls.
type Governor struct {
	mu      sync.Mutex
	cfg     Config
	now     func() time.Time
	started time.Time
	history []Action
	// counters are lifetime totals for the turn, not windowed.
	searches int
	reads    int
	edits    int
	runs     int
	edited   bool
	// noProgress is the current run of consecutive empty actions.
	noProgress int
	// distinctReads counts the unique fingerprints that were read, which is
	// what readiness credits.
	distinctReads map[string]struct{}
	// highestIssued is the highest steering level already delivered, so each
	// escalation speaks once and never replays a gentler note.
	highestIssued Level
}

// New creates a governor for one turn.
func New(cfg Config) *Governor {
	cfg = cfg.withDefaults()
	now := time.Now
	return &Governor{
		cfg:           cfg,
		now:           now,
		started:       now(),
		distinctReads: make(map[string]struct{}),
	}
}

// SetClock replaces the governor's clock, for tests.
func (g *Governor) SetClock(now func() time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.now = now
	g.started = now()
}

// Observe records an action and returns the resulting assessment.
func (g *Governor) Observe(action Action) Report {
	if g == nil {
		return Report{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if action.At.IsZero() {
		action.At = g.now()
	}
	switch action.Kind {
	case ActionSearch:
		g.searches++
	case ActionRead:
		g.reads++
	case ActionEdit:
		g.edits++
		g.edited = true
	case ActionRun:
		g.runs++
	}
	if action.Empty {
		g.noProgress++
	} else {
		g.noProgress = 0
	}
	if action.Kind == ActionRead && !action.Empty && action.Fingerprint != "" {
		g.distinctReads[action.Fingerprint] = struct{}{}
	}

	g.history = append(g.history, action)
	if len(g.history) > g.cfg.HistorySize {
		g.history = g.history[len(g.history)-g.cfg.HistorySize:]
	}
	return g.reportLocked()
}

// Report returns the current assessment without recording anything.
func (g *Governor) Report() Report {
	if g == nil {
		return Report{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reportLocked()
}

// reportLocked computes the assessment. Callers hold the lock.
func (g *Governor) reportLocked() Report {
	report := Report{
		Searches:          g.searches,
		Reads:             g.reads,
		Edits:             g.edits,
		Runs:              g.runs,
		Elapsed:           g.now().Sub(g.started),
		NoProgressActions: g.noProgress,
		Edited:            g.edited,
		Readiness:         g.readinessLocked(),
	}
	if g.cfg.Disabled {
		return report
	}

	repeats := g.repeatsLocked()
	report.RepeatedSearches = repeats.searches
	report.RepeatedReads = repeats.reads
	report.Progress = g.progressLocked()

	pressure, reason := g.pressureLocked(repeats)
	report.Pressure = pressure
	report.Reason = reason
	report.Level = g.levelFor(pressure)
	if report.Level == LevelNone {
		report.Reason = "within normal bounds"
	}
	return report
}

// repeatCounts counts how many times the most-repeated search and read appear
// in the window.
type repeatCounts struct {
	searches int
	reads    int
}

// repeatsLocked finds the worst repetition in the history.
func (g *Governor) repeatsLocked() repeatCounts {
	searches := make(map[string]int)
	reads := make(map[string]int)
	var out repeatCounts
	for _, action := range g.history {
		if action.Fingerprint == "" || action.Empty {
			continue
		}
		switch action.Kind {
		case ActionSearch:
			searches[action.Fingerprint]++
			if searches[action.Fingerprint] > out.searches {
				out.searches = searches[action.Fingerprint]
			}
		case ActionRead:
			reads[action.Fingerprint]++
			if reads[action.Fingerprint] > out.reads {
				out.reads = reads[action.Fingerprint]
			}
		}
	}
	// A repeat only counts once it crosses the threshold, and only the part
	// past the threshold is pressure. Reading one file twice is normal.
	if out.searches < g.cfg.RepeatedSearchThreshold {
		out.searches = 0
	}
	if out.reads < g.cfg.RepeatedReadThreshold {
		out.reads = 0
	}
	return out
}

// progressLocked returns the share of recent actions that produced something.
func (g *Governor) progressLocked() float64 {
	if len(g.history) == 0 {
		return 0
	}
	productive := 0
	for _, action := range g.history {
		if action.Kind == ActionEdit || action.Kind == ActionRun {
			productive++
			continue
		}
		if !action.Empty {
			productive++
		}
	}
	return float64(productive) / float64(len(g.history))
}

// pressureLocked computes the pressure score and a one-clause explanation.
//
// Pressure is additive across independent symptoms, then multiplied by elapsed
// time. Additive rather than multiplicative so one severe symptom is enough to
// steer, and time-bounded on the top so a long, genuinely productive turn never
// becomes "critical".
func (g *Governor) pressureLocked(repeats repeatCounts) (float64, string) {
	var pressure float64
	var reasons []string

	if repeats.searches > 0 {
		excess := repeats.searches - g.cfg.RepeatedSearchThreshold + 1
		pressure += ladder(excess, 0.25, 0.15)
		reasons = append(reasons, "repeated searches")
	}
	if repeats.reads > 0 {
		excess := repeats.reads - g.cfg.RepeatedReadThreshold + 1
		pressure += ladder(excess, 0.25, 0.15)
		reasons = append(reasons, "repeated reads")
	}
	if g.noProgress >= g.cfg.NoProgressThreshold {
		excess := g.noProgress - g.cfg.NoProgressThreshold + 1
		pressure += ladder(excess, 0.2, 0.2)
		reasons = append(reasons, "no new information")
	}
	// Reading without ever changing anything is its own signal, but only
	// after enough reads to be meaningful: two reads is a look, not a storm.
	if !g.edited && g.reads >= 6 && g.edits == 0 {
		pressure += min(0.05*float64(g.reads-4), 0.25)
		reasons = append(reasons, "reads without an edit")
	}

	// Elapsed time multiplies; it never adds a level on its own, so a long
	// turn that keeps landing changes is untouched.
	elapsed := g.now().Sub(g.started)
	if elapsed >= g.cfg.TimeCritical {
		pressure *= 1.5
		reasons = append(reasons, "exploring "+elapsed.Round(time.Second).String())
	} else if elapsed >= g.cfg.TimeWarning {
		pressure *= 1.25
		reasons = append(reasons, "exploring "+elapsed.Round(time.Second).String())
	}

	pressure = math.Min(pressure, 1)
	if pressure <= 0 || len(reasons) == 0 {
		return 0, "within normal bounds"
	}
	return pressure, strings.Join(reasons, ", ")
}

// ladder maps "how far past the threshold" to a pressure contribution. The
// first repeat already lands at the soft threshold, and each further one adds a
// fixed step so sustained repetition walks the escalation ladder instead of
// pinning at the first level. The cap keeps a single symptom from reaching the
// refusal level on its own: refusing an action takes more than one kind of
// evidence.
func ladder(excess int, base, step float64) float64 {
	contribution := base + step*float64(excess-1)
	return math.Min(contribution, 0.8)
}

// readinessLocked estimates how ready the turn is to act: distinct files read,
// plus edits, minus emptiness.
func (g *Governor) readinessLocked() float64 {
	score := float64(len(g.distinctReads))*0.15 + float64(g.edits)*0.35 + float64(g.runs)*0.05
	score -= float64(g.noProgress) * 0.1
	if score < 0 {
		return 0
	}
	return math.Min(score, 1)
}

// levelFor maps pressure to a level.
func (g *Governor) levelFor(pressure float64) Level {
	switch {
	case pressure >= g.cfg.HardThreshold:
		return LevelHard
	case pressure >= g.cfg.StrongThreshold:
		return LevelStrong
	case pressure >= g.cfg.ModerateThreshold:
		return LevelModerate
	case pressure >= g.cfg.SoftThreshold:
		return LevelSoft
	default:
		return LevelNone
	}
}

// TakeNote returns the steering note for the current level and marks it
// delivered, so a level is explained once instead of on every action. It
// returns "" when there is nothing new to say.
func (g *Governor) TakeNote() (Level, string) {
	if g == nil {
		return LevelNone, ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cfg.Disabled {
		return LevelNone, ""
	}
	report := g.reportLocked()
	if report.Level == LevelNone || report.Level <= g.highestIssued {
		return LevelNone, ""
	}
	// Delivering a level also covers every level below it, so an escalation
	// says only the new thing and never replays the gentler text.
	g.highestIssued = report.Level
	return report.Level, SteeringNote(report)
}

// Reset clears the turn state, keeping the configuration.
func (g *Governor) Reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.history = nil
	g.searches, g.reads, g.edits, g.runs = 0, 0, 0, 0
	g.edited = false
	g.noProgress = 0
	g.distinctReads = make(map[string]struct{})
	g.highestIssued = LevelNone
	g.started = g.now()
}
