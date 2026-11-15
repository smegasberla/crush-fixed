package governor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestGovernor returns a governor with a controllable clock.
func newTestGovernor(t *testing.T, cfg Config) (*Governor, *time.Time) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	clock := now
	g := New(cfg)
	g.SetClock(func() time.Time { return clock })
	return g, &clock
}

func TestDefaultConfigThresholds(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	require.Equal(t, 0.25, cfg.SoftThreshold)
	require.Equal(t, 0.45, cfg.ModerateThreshold)
	require.Equal(t, 0.65, cfg.StrongThreshold)
	require.Equal(t, 0.85, cfg.HardThreshold)
	require.Equal(t, 3, cfg.RepeatedSearchThreshold)
	require.Equal(t, 3, cfg.RepeatedReadThreshold)
	require.Equal(t, 5, cfg.NoProgressThreshold)
	require.Equal(t, 240*time.Second, cfg.TimeWarning)
	require.Equal(t, 600*time.Second, cfg.TimeCritical)
	require.Equal(t, 40, cfg.HistorySize)
}

func TestProductiveTurnStaysSilent(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	var level Level
	for i := range 12 {
		report := g.Observe(Action{Kind: ActionRead, Fingerprint: string(rune('a' + i))})
		level = report.Level
		if i%2 == 0 {
			report = g.Observe(Action{Kind: ActionEdit, Fingerprint: "edit"})
			level = report.Level
		}
		report = g.Observe(Action{Kind: ActionRun, Fingerprint: "build"})
		level = report.Level
		require.Equal(t, LevelNone, report.Level, "a turn that reads distinct files and edits must never steer")
	}
	require.Equal(t, LevelNone, level)
}

func TestRepeatedSearchesRaisePressure(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	var report Report
	for range 4 {
		report = g.Observe(Action{Kind: ActionSearch, Fingerprint: "same query"})
	}
	require.Equal(t, 4, report.RepeatedSearches)
	require.Greater(t, report.Pressure, 0.0)
	require.GreaterOrEqual(t, report.Level, LevelSoft)
	require.Contains(t, report.Reason, "repeated searches")
}

func TestRepeatedReadsRaisePressure(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	var report Report
	for range 5 {
		report = g.Observe(Action{Kind: ActionRead, Fingerprint: "internal/a.go:1-200"})
	}
	require.Equal(t, 5, report.RepeatedReads)
	require.GreaterOrEqual(t, report.Level, LevelSoft)
}

func TestTwoReadsOfOneFileIsNotPressure(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	g.Observe(Action{Kind: ActionRead, Fingerprint: "a.go"})
	report := g.Observe(Action{Kind: ActionRead, Fingerprint: "a.go"})
	require.Zero(t, report.RepeatedReads, "a second look at a file is normal")
	require.Equal(t, LevelNone, report.Level)
}

func TestNoProgressEscalates(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	var report Report
	for range 6 {
		report = g.Observe(Action{Kind: ActionOther, Empty: true, Fingerprint: "failing"})
	}
	require.Equal(t, 6, report.NoProgressActions)
	require.GreaterOrEqual(t, report.Level, LevelSoft)
	require.Contains(t, report.Reason, "no new information")

	// Sustained emptiness escalates rather than pinning at the first level.
	for range 4 {
		report = g.Observe(Action{Kind: ActionOther, Empty: true, Fingerprint: "failing"})
	}
	require.GreaterOrEqual(t, report.Level, LevelModerate)

	// A productive action clears the run.
	report = g.Observe(Action{Kind: ActionEdit, Fingerprint: "edit"})
	require.Zero(t, report.NoProgressActions)
}

func TestPressureReachesHardOnSustainedRepetition(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	var report Report
	for range 20 {
		report = g.Observe(Action{Kind: ActionSearch, Fingerprint: "loop"})
		report = g.Observe(Action{Kind: ActionRead, Fingerprint: "same.go"})
		report = g.Observe(Action{Kind: ActionOther, Empty: true, Fingerprint: "fail"})
	}
	require.Equal(t, LevelHard, report.Level)
	require.GreaterOrEqual(t, report.Pressure, 0.85)
}

func TestTimeMultipliesButNeverDecides(t *testing.T) {
	t.Parallel()

	g, clock := newTestGovernor(t, DefaultConfig())
	// Read one distinct file and edit: plenty of progress, no repetition.
	g.Observe(Action{Kind: ActionRead, Fingerprint: "a.go"})
	base := g.Report()
	require.Equal(t, LevelNone, base.Level)

	*clock = clock.Add(700 * time.Second)
	g.Observe(Action{Kind: ActionEdit, Fingerprint: "edit"})
	report := g.Report()
	require.Equal(t, LevelNone, report.Level, "elapsed time alone never raises the level")
	require.Equal(t, base.Pressure, report.Pressure, "there is no pressure for a multiplier to amplify")
	require.Equal(t, "within normal bounds", report.Reason)
}

func TestTimeMultiplierAmplifiesExistingPressure(t *testing.T) {
	t.Parallel()

	fast, _ := newTestGovernor(t, DefaultConfig())
	slow, clock := newTestGovernor(t, DefaultConfig())

	for _, g := range []*Governor{fast, slow} {
		for range 4 {
			g.Observe(Action{Kind: ActionSearch, Fingerprint: "q"})
		}
	}
	fastPressure := fast.Report().Pressure
	*clock = clock.Add(700 * time.Second)
	slow.Observe(Action{Kind: ActionOther, Fingerprint: "x"})
	require.Greater(t, slow.Report().Pressure, fastPressure)
}

func TestReadinessRisesWithDistinctReadsAndEdits(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	require.Zero(t, g.Report().Readiness)

	g.Observe(Action{Kind: ActionRead, Fingerprint: "a.go"})
	first := g.Report().Readiness
	require.Positive(t, first)

	g.Observe(Action{Kind: ActionRead, Fingerprint: "b.go"})
	require.Greater(t, g.Report().Readiness, first)

	// Reading the same file again does not add readiness.
	g.Observe(Action{Kind: ActionRead, Fingerprint: "b.go"})
	require.InDelta(t, first*2, g.Report().Readiness, 0.2)

	g.Observe(Action{Kind: ActionEdit, Fingerprint: "edit"})
	require.True(t, g.Report().Edited)
	require.Greater(t, g.Report().Readiness, first*2)
}

func TestTakeNoteDeliversOncePerLevel(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	for range 4 {
		g.Observe(Action{Kind: ActionSearch, Fingerprint: "q"})
	}

	level, text := g.TakeNote()
	require.GreaterOrEqual(t, level, LevelSoft)
	require.NotEmpty(t, text)

	// The same level does not speak twice.
	level, text = g.TakeNote()
	require.Equal(t, LevelNone, level)
	require.Empty(t, text)

	// Escalating speaks again, with different text. Refusing an action takes
	// more than one kind of evidence, so the ladder needs a second symptom.
	for range 30 {
		g.Observe(Action{Kind: ActionSearch, Fingerprint: "q", Empty: true})
	}
	level, text = g.TakeNote()
	require.Equal(t, LevelHard, level)
	require.NotEmpty(t, text)
	require.Contains(t, text, "Act on what you have")
}

func TestTakeNoteSilentBelowSoft(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	g.Observe(Action{Kind: ActionRead, Fingerprint: "a.go"})
	level, text := g.TakeNote()
	require.Equal(t, LevelNone, level)
	require.Empty(t, text)
}

func TestDisabledGovernorNeverSteers(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Disabled = true
	g := New(cfg)
	for range 50 {
		g.Observe(Action{Kind: ActionSearch, Fingerprint: "q", Empty: true})
	}
	report := g.Report()
	require.Equal(t, LevelNone, report.Level)
	require.Zero(t, report.Pressure)
	_, text := g.TakeNote()
	require.Empty(t, text)
}

func TestNilGovernorIsSafe(t *testing.T) {
	t.Parallel()

	var g *Governor
	require.Equal(t, Report{}, g.Observe(Action{}))
	require.Equal(t, Report{}, g.Report())
	_, text := g.TakeNote()
	require.Empty(t, text)
	g.Reset()
}

func TestResetClearsTheTurn(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	for range 6 {
		g.Observe(Action{Kind: ActionSearch, Fingerprint: "q"})
	}
	require.Greater(t, g.Report().Searches, 0)

	g.Reset()
	report := g.Report()
	require.Zero(t, report.Searches)
	require.Zero(t, report.Pressure)
	require.Equal(t, LevelNone, report.Level)
}

func TestHistoryIsBounded(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HistorySize = 5
	g, _ := newTestGovernor(t, cfg)
	for i := range 50 {
		g.Observe(Action{Kind: ActionRead, Fingerprint: string(rune('a' + i%20))})
	}
	// Totals are lifetime, so they keep counting.
	require.Equal(t, 50, g.Report().Reads)
	// But the window is bounded, so an ancient repeat cannot dominate.
	require.LessOrEqual(t, g.Report().RepeatedReads, 5)
}

func TestConcurrentObserveIsSafe(t *testing.T) {
	t.Parallel()

	g, _ := newTestGovernor(t, DefaultConfig())
	done := make(chan struct{})
	for i := range 8 {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			g.Observe(Action{Kind: ActionSearch, Fingerprint: string(rune('a' + i))})
			_ = g.Report()
			_, _ = g.TakeNote()
		}(i)
	}
	for range 8 {
		<-done
	}
	require.Equal(t, 8, g.Report().Searches)
}

func TestLevelString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "none", LevelNone.String())
	require.Equal(t, "soft", LevelSoft.String())
	require.Equal(t, "moderate", LevelModerate.String())
	require.Equal(t, "strong", LevelStrong.String())
	require.Equal(t, "hard", LevelHard.String())
}

func TestActionKindString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "read", ActionRead.String())
	require.Equal(t, "search", ActionSearch.String())
	require.Equal(t, "edit", ActionEdit.String())
	require.Equal(t, "run", ActionRun.String())
	require.Equal(t, "empty", ActionEmpty.String())
	require.Equal(t, "other", ActionOther.String())
}

func TestSteeringNotesAreVerbatim(t *testing.T) {
	t.Parallel()

	require.Contains(t, SteeringNote(Report{Level: LevelSoft}), "diminishing returns")
	require.Contains(t, SteeringNote(Report{Level: LevelModerate}), "stop re-asking what you have already answered")
	require.Contains(t, SteeringNote(Report{Level: LevelStrong}), "will now be blocked")
	require.Contains(t, SteeringNote(Report{Level: LevelHard}), "Act on what you have")
	require.Empty(t, SteeringNote(Report{Level: LevelNone}))

	require.Contains(t, GatheredNote(), "gathered substantial context")
	require.Contains(t, StopRequestedNote(), "The user asked to stop")
	require.Contains(t, QuestionNote(), "Answer it in your next reply first")
	require.Contains(t, ScopeAddedNote(), "The user added scope")
	require.Contains(t, RejectedApproachNote(), "Discard the plan")
	require.Contains(t, RequirementChangedNote(), "newest message is authoritative")
	require.Contains(t, SettledStateNote("the API returns 204"), "the API returns 204")
	require.Contains(t, SettledStateNote(""), "re-deliberating")
	require.Contains(t, AlreadyRefutedNote("the cache is stale"), "the cache is stale")
}

func TestPressureIsCappedAtOne(t *testing.T) {
	t.Parallel()

	g, clock := newTestGovernor(t, DefaultConfig())
	for range 200 {
		g.Observe(Action{Kind: ActionSearch, Fingerprint: "q", Empty: true})
		g.Observe(Action{Kind: ActionRead, Fingerprint: "same", Empty: true})
	}
	*clock = clock.Add(2000 * time.Second)
	report := g.Report()
	require.LessOrEqual(t, report.Pressure, 1.0)
	require.Equal(t, LevelHard, report.Level)
}
