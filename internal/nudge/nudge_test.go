package nudge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	return New(DefaultConfig())
}

func TestDefaultConfigThresholds(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	require.Equal(t, 4, cfg.ReadStormRounds)
	require.Equal(t, 3, cfg.SingleCallRounds)
	require.Equal(t, 3, cfg.NoProgressRounds)
	require.Equal(t, 1, cfg.RepeatCallAfter)
	require.Equal(t, 2, cfg.BackgroundPolls)
	require.Equal(t, 4000, cfg.ReasoningOverrunChars)
	require.Equal(t, 5, cfg.SmallestEditReads)
	require.Equal(t, 6, cfg.SmallestEditRounds)
}

func TestCleanRoundProducesNothing(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{
		Round:       2,
		TotalRounds: 2,
		ProseChars:  400,
		ToolCalls:   2,
		Edits:       1,
		Runs:        1,
	})
	require.Empty(t, notes, "a productive round must never be scolded")
}

func TestRepeatCallNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 2, RepeatedCallCount: 2})
	require.Len(t, notes, 1)
	require.Equal(t, ReasonRepeatCall, notes[0].Reason)
	require.Contains(t, notes[0].Text, "repeating it will not change what it returns")
}

func TestMalformedNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 1, MalformedCalls: 2})
	require.Len(t, notes, 1)
	require.Equal(t, ReasonFormat, notes[0].Reason)
	require.Contains(t, notes[0].Text, "could not be parsed")
}

func TestBackgroundPollingNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	require.Empty(t, e.Evaluate(Counters{Round: 1, BackgroundPolls: 1, ProseChars: 50}))

	notes := e.Evaluate(Counters{Round: 2, BackgroundPolls: 2, ProseChars: 50})
	require.Len(t, notes, 1)
	require.Equal(t, ReasonBackgroundPolling, notes[0].Reason)
	require.Contains(t, notes[0].Text, "Do not check it on a timer")
	require.Contains(t, notes[0].Text, "Three ways forward")
}

func TestNoProgressNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	require.Empty(t, e.Evaluate(Counters{Round: 1, ConsecutiveNoProgress: 2, ProseChars: 100}))

	notes := e.Evaluate(Counters{Round: 2, ConsecutiveNoProgress: 3, ProseChars: 100})
	require.Equal(t, ReasonNoProgress, notes[0].Reason)
	require.Contains(t, notes[0].Text, "no new progress")
}

func TestStutterNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 1, RepeatedProseCount: 1, ProseChars: 100})
	require.Equal(t, ReasonStutter, notes[0].Reason)
	require.Contains(t, notes[0].Text, "repeated itself")
}

func TestEmptyAnswerNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 1})
	require.Equal(t, ReasonEmptyAnswer, notes[0].Reason)
	require.Contains(t, notes[0].Text, "reached the user as NOTHING")

	// Prose or a tool call means the round delivered something.
	require.Empty(t, e.Evaluate(Counters{Round: 2, ProseChars: 10}))
}

func TestReasoningOverrunNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 1, ReasoningChars: 5000})
	require.Equal(t, ReasonReasoningOverrun, notes[0].Reason)
	require.Contains(t, notes[0].Text, "5000 characters of reasoning and no action")

	// Thinking followed by an action is normal reasoning.
	require.Empty(t, e.Evaluate(Counters{Round: 2, ReasoningChars: 5000, ToolCalls: 1}))
}

func TestReadStormNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	require.Empty(t, e.Evaluate(Counters{Round: 1, ConsecutiveReadOnly: 3, ProseChars: 10}))
	notes := e.Evaluate(Counters{Round: 2, ConsecutiveReadOnly: 4, ProseChars: 10})
	require.Equal(t, ReasonReadStorm, notes[0].Reason)
	require.Contains(t, notes[0].Text, "Reading has run ahead of changing")
}

func TestSmallestEditNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 8, TotalRounds: 8, ConsecutiveReadOnly: 5, ProseChars: 10})
	require.Equal(t, ReasonSmallestEdit, notes[0].Reason)
	require.Contains(t, notes[0].Text, "Stop gathering and act")
}

func TestParallelNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	require.Empty(t, e.Evaluate(Counters{Round: 1, ConsecutiveSingleCall: 2, ProseChars: 10}))
	notes := e.Evaluate(Counters{Round: 2, ConsecutiveSingleCall: 3, ProseChars: 10})
	require.Equal(t, ReasonParallel, notes[0].Reason)
	require.Contains(t, notes[0].Text, "exactly ONE tool call")
}

func TestRemainingWorkNudge(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	require.Empty(t, e.Evaluate(Counters{Round: 1, TodosOpen: 2, TodosTotal: 3, ProseChars: 10}))
	notes := e.Evaluate(Counters{Round: 4, TodosOpen: 2, TodosTotal: 3, ProseChars: 10})
	require.Equal(t, ReasonRemainingWork, notes[0].Reason)
	require.Contains(t, notes[0].Text, "2 of 3 plan step(s) are not completed")
}

func TestFalseCompletionIsRefused(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{
		Round:            3,
		ClaimsCompletion: true,
		ProseChars:       200,
		TodosTotal:       0,
		TodosOpen:        0,
		Edits:            0,
		Runs:             0,
		ToolCalls:        0,
	})
	require.Len(t, notes, 1)
	require.Equal(t, ReasonFalseCompletion, notes[0].Reason)
	require.Equal(t, 2, notes[0].Severity)
	require.Contains(t, notes[0].Text, "completion claims edits")
}

func TestFalseCompletionAllowsAnEdit(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 3, Planning: true, ClaimsCompletion: true, TodosTotal: 2, Edits: 1, ProseChars: 100})
	for _, note := range notes {
		require.NotEqual(t, ReasonFalseCompletion, note.Reason)
	}
}

func TestFalseCompletionIsAlwaysRefused(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	counters := Counters{Round: 3, ClaimsCompletion: true, TodosTotal: 1, ProseChars: 100}
	require.Len(t, e.Evaluate(counters), 1)
	// Refusals are not de-duplicated: a repeated claim is refused again.
	require.Len(t, e.Evaluate(counters), 1)
}

func TestRecapOnlyRefused(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{Round: 2, ProseChars: 120, RecapBlock: true})
	require.Equal(t, ReasonRecapOnly, notes[0].Reason)
	require.Equal(t, 2, notes[0].Severity)
	require.Contains(t, notes[0].Text, "NOT A FINISHED ANSWER")
}

func TestPlanOpenUnfinishedRefused(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	notes := e.Evaluate(Counters{
		Round:            3,
		Planning:         true,
		ClaimsCompletion: true,
		ProseChars:       100,
		TodosTotal:       2,
		TodosOpen:        0,
		RecapBlock:       true,
	})
	require.Equal(t, ReasonPlanOpenUnfinished, notes[0].Reason)
	require.Equal(t, 2, notes[0].Severity)
	require.Contains(t, notes[0].Text, "no plan exists and no change has landed")
}

func TestNotesAreNotRepeatedImmediately(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	counters := Counters{Round: 2, ConsecutiveReadOnly: 5, ProseChars: 20}
	require.Len(t, e.Evaluate(counters), 1)

	counters.Round = 3
	require.Empty(t, e.Evaluate(counters), "the same state does not speak twice")

	counters.Round = 12
	require.Len(t, e.Evaluate(counters), 1, "a state that persists is worth restating eventually")
}

func TestDisabledEngineIsSilent(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Disabled = true
	e := New(cfg)
	require.Empty(t, e.Evaluate(Counters{Round: 20, ConsecutiveReadOnly: 10, RepeatedCallCount: 5}))
}

func TestNilEngineIsSafe(t *testing.T) {
	t.Parallel()

	var e *Engine
	require.Empty(t, e.Evaluate(Counters{Round: 1}))
	e.Reset()
}

func TestResetClearsHistory(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	counters := Counters{Round: 2, ConsecutiveReadOnly: 5, ProseChars: 20}
	require.Len(t, e.Evaluate(counters), 1)
	e.Reset()
	require.Len(t, e.Evaluate(counters), 1, "after a reset the note is deliverable again")
}

func TestHighestSeverityWins(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	// A false completion and a read storm are both true; only the refusal is
	// delivered, because two competing instructions are worse than one.
	notes := e.Evaluate(Counters{
		Round:               3,
		ClaimsCompletion:    true,
		TodosTotal:          1,
		ConsecutiveReadOnly: 10,
		ProseChars:          100,
	})
	require.Len(t, notes, 1)
	require.Equal(t, ReasonFalseCompletion, notes[0].Reason)
}

func TestLooksLikeRecap(t *testing.T) {
	t.Parallel()

	recaps := []string{
		"### Suggested next steps\n- run the test",
		"## Next steps",
		"Next steps: run the build",
		"Summary of changes: none",
		"In summary, the work is done.",
		"To summarise, nothing changed.",
		"  ### SUGGESTED NEXT STEPS",
	}
	for _, text := range recaps {
		require.True(t, LooksLikeRecap(text), "should look like a recap: %q", text)
	}

	answers := []string{
		"The bug is in the wait loop.",
		"Here is the fix, which also covers the retry path.",
		"",
		"   ",
		// A marker that is not at the start is content, not a wrap-up.
		"The change is small. Next steps: verify with go test.",
	}
	for _, text := range answers {
		require.False(t, LooksLikeRecap(text), "should not look like a recap: %q", text)
	}
}

func TestReasoningScoreMetrics(t *testing.T) {
	t.Parallel()

	score := Score{}
	score = SummarizeReasoning(score, 1000, 200, 500, true, 1)
	score = SummarizeReasoning(score, 2000, 600, 300, false, 3)

	require.Equal(t, 2, score.Rounds)
	require.Equal(t, 2, score.ThinkingRounds)
	require.Equal(t, 1, score.ProgressRounds)
	require.Equal(t, 3000, score.ReasonChars)
	require.Equal(t, 4, score.Restarts)
	require.Equal(t, 600, score.WorstTailChars)
	require.InDelta(t, 1.0, score.TailRatio, 0.001, "a tail larger than the answer is clamped at 1")

	// 4 restarts over 3k characters is 1.33 per 1k, below the 2/1k bar, but
	// two thinking rounds that landed one change out of two is not yet a
	// spin either: the score reports, the caller decides.
	require.InDelta(t, 1.333, score.RestartDensity(), 0.01)
	require.False(t, score.NeedsRecovery())

	// Three thinking rounds with no progress crosses the bar.
	spinning := SummarizeReasoning(Score{}, 800, 100, 0, false, 3)
	require.True(t, spinning.NeedsRecovery())
}

func TestScoreNeedsRecovery(t *testing.T) {
	t.Parallel()

	// High restart density alone is enough.
	require.True(t, Score{ReasonChars: 1000, Restarts: 3}.NeedsRecovery())

	// Thinking without progress is enough.
	require.True(t, Score{ThinkingRounds: 3, ProgressRounds: 0}.NeedsRecovery())

	// Clean thinking is not.
	require.False(t, Score{ReasonChars: 4000, Restarts: 1, ThinkingRounds: 3, ProgressRounds: 3}.NeedsRecovery())
	require.False(t, Score{}.NeedsRecovery())
}

func TestReasoningShare(t *testing.T) {
	t.Parallel()

	require.Zero(t, Score{}.ReasoningShare())
	share := Score{ReasonChars: 1000, Rounds: 1}.ReasoningShare()
	require.Greater(t, share, 0.0)
	require.Less(t, share, 1.0)
}

func TestReasoningNotes(t *testing.T) {
	t.Parallel()

	require.Contains(t, ReasoningNote(), "has not changed the observed outcome")
	require.Contains(t, ReassessNote(), "Reassess the current state")
	require.Contains(t, ReasoningRefusedNote(), "was refused by the provider")
}

func TestClamp(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0.0, Clamp(-1))
	require.Equal(t, 0.5, Clamp(0.5))
	require.Equal(t, 1.0, Clamp(2))
}

func TestSummarizeReasoningWithoutAnswer(t *testing.T) {
	t.Parallel()

	score := SummarizeReasoning(Score{}, 500, 300, 0, false, 0)
	require.Equal(t, 300, score.WorstTailChars)
	require.Equal(t, 1.0, score.TailRatio, "without an answer any tail is a full-ratio tail")
}

func TestEveryReasonHasAText(t *testing.T) {
	t.Parallel()

	e := newTestEngine(t)
	cases := map[Reason]Counters{
		ReasonRepeatCall:        {Round: 1, RepeatedCallCount: 3, ProseChars: 50},
		ReasonFormat:            {Round: 1, MalformedCalls: 1, ProseChars: 50},
		ReasonBackgroundPolling: {Round: 1, BackgroundPolls: 5, ProseChars: 50},
		ReasonNoProgress:        {Round: 1, ConsecutiveNoProgress: 5, ProseChars: 50},
		ReasonStutter:           {Round: 1, RepeatedProseCount: 2},
		ReasonEmptyAnswer:       {Round: 1},
		ReasonReasoningOverrun:  {Round: 1, ReasoningChars: 9000},
		// Below the "act now" threshold, so the read-storm note is the one
		// that fires for read-only rounds.
		ReasonReadStorm:     {Round: 2, TotalRounds: 2, ConsecutiveReadOnly: 5, ProseChars: 50},
		ReasonSmallestEdit:  {Round: 9, TotalRounds: 9, ConsecutiveReadOnly: 9, ProseChars: 50},
		ReasonParallel:      {Round: 1, ConsecutiveSingleCall: 9, ProseChars: 50},
		ReasonRemainingWork: {Round: 9, TodosOpen: 1, TodosTotal: 2, ProseChars: 50},
	}
	for want, counters := range cases {
		notes := e.Evaluate(counters)
		require.Len(t, notes, 1, "expected exactly one note for %s", want)
		require.Equal(t, want, notes[0].Reason)
		require.NotEmpty(t, strings.TrimSpace(notes[0].Text))
		require.Equal(t, 1, notes[0].Severity, "advisory notes have severity 1")
		e.Reset()
	}
}
