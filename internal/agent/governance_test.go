package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/governor"
	"github.com/stretchr/testify/require"
)

func governanceForTest(t *testing.T, settings *config.GovernanceOptions) *governanceState {
	t.Helper()
	return newGovernanceState(&GovernanceOptions{Settings: settings})
}

func TestNewGovernanceStateDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, newGovernanceState(nil))
	require.Nil(t, newGovernanceState(&GovernanceOptions{}), "nil settings disable the layer")

	off := false
	require.Nil(t, newGovernanceState(&GovernanceOptions{
		Settings: &config.GovernanceOptions{Enabled: &off},
	}))

	state := governanceForTest(t, &config.GovernanceOptions{})
	require.NotNil(t, state)
	require.True(t, state.enabled())
	require.True(t, state.completionGateEnabled())
	require.True(t, state.reasoningMetricsEnabled())
	require.Zero(t, state.maxRounds())
}

func TestGovernorConfigFollowsSettings(t *testing.T) {
	t.Parallel()

	soft := 0.3
	searchRepeats := 2
	history := 12
	state := governanceForTest(t, &config.GovernanceOptions{
		SoftThreshold:           &soft,
		RepeatedSearchThreshold: &searchRepeats,
		HistorySize:             &history,
	})
	cfg := state.governorConfig()
	require.Equal(t, 0.3, cfg.SoftThreshold)
	require.Equal(t, 2, cfg.RepeatedSearchThreshold)
	require.Equal(t, 12, cfg.HistorySize)
	// Untouched fields keep their defaults.
	require.Equal(t, governor.DefaultConfig().HardThreshold, cfg.HardThreshold)
	require.False(t, cfg.Disabled)

	nudgeCfg := state.nudgeThresholds()
	require.Equal(t, 1, nudgeCfg.RepeatCallAfter, "the nudge follows the repetition setting")
	require.False(t, nudgeCfg.Disabled)
}

func TestGovernanceStateDisabledConfigs(t *testing.T) {
	t.Parallel()

	var nilState *governanceState
	require.False(t, nilState.enabled())
	require.True(t, nilState.governorConfig().Disabled)
	require.True(t, nilState.nudgeThresholds().Disabled)
	require.False(t, nilState.completionGateEnabled())
	require.False(t, nilState.reasoningMetricsEnabled())
	require.Zero(t, nilState.maxRounds())
}

func TestTurnGovernorNilWithoutState(t *testing.T) {
	t.Parallel()

	var nilState *governanceState
	require.Nil(t, newTurnGovernor(nilState))
}

func TestTurnGovernorRoundLifecycle(t *testing.T) {
	t.Parallel()

	state := governanceForTest(t, &config.GovernanceOptions{})
	turn := newTurnGovernor(state)
	require.NotNil(t, turn)

	turn.beginRound()
	require.Equal(t, 1, turn.counters.Round)

	turn.observeReasoning("thinking about the build")
	turn.observeProse("the build fails")
	require.Equal(t, len("thinking about the build"), turn.counters.ReasoningChars)
	require.Equal(t, len("the build fails"), turn.counters.ProseChars)

	turn.observeToolResult("edit", false, "edited a.go")
	require.Equal(t, 1, turn.counters.Edits)

	turn.beginRound()
	require.Equal(t, 2, turn.counters.Round)
	require.Zero(t, turn.counters.ReasoningChars)
	require.Zero(t, turn.counters.ProseChars)
}

func TestTurnGovernorRefusesRepeatOnlyAtHard(t *testing.T) {
	t.Parallel()

	state := governanceForTest(t, &config.GovernanceOptions{})
	turn := newTurnGovernor(state)

	// A first call is never refused.
	require.False(t, turn.observeToolCall("bash", `{"command":"go build"}`))
	// A repeat while pressure is low is allowed: the model may legitimately
	// re-run a check after an edit.
	require.False(t, turn.observeToolCall("bash", `{"command":"go build"}`))

	// Drive pressure past the hard threshold with repeated identical reads.
	for range 40 {
		turn.observeToolCall("view", `{"file_path":"same.go"}`)
	}
	// An already-answered call is now refused.
	require.True(t, turn.observeToolCall("view", `{"file_path":"same.go"}`))
	// A genuinely new query still runs: the governor blocks redundancy, not
	// work.
	require.False(t, turn.observeToolCall("view", `{"file_path":"other.go"}`))
}

func TestTurnGovernorNotesAreQueuedAndTaken(t *testing.T) {
	t.Parallel()

	state := governanceForTest(t, &config.GovernanceOptions{})
	turn := newTurnGovernor(state)

	turn.beginRound()
	notes := turn.endRound(roundOutcome{consecutiveReadOnly: 5, repeatedProse: 0})
	require.NotEmpty(t, notes)

	queued := turn.takeNotes()
	require.Equal(t, notes, queued)
	require.Empty(t, turn.takeNotes(), "notes are delivered once")
}

func TestGovernanceNotesText(t *testing.T) {
	t.Parallel()

	require.Empty(t, governanceNotesText(nil))
	text := governanceNotesText([]string{"first", "second"})
	require.Contains(t, text, "<governance_notes>")
	require.Contains(t, text, "first")
	require.Contains(t, text, "second")
	require.Contains(t, text, "</governance_notes>")
}

func TestActionKindForTool(t *testing.T) {
	t.Parallel()

	require.Equal(t, governor.ActionRead, actionKindForTool("view"))
	require.Equal(t, governor.ActionRead, actionKindForTool("grep"))
	require.Equal(t, governor.ActionSearch, actionKindForTool("web_search"))
	require.Equal(t, governor.ActionEdit, actionKindForTool("edit"))
	require.Equal(t, governor.ActionEdit, actionKindForTool("multiedit"))
	require.Equal(t, governor.ActionRun, actionKindForTool("bash"))
	require.Equal(t, governor.ActionOther, actionKindForTool("todos"))
}

func TestRoundTrackerCounts(t *testing.T) {
	t.Parallel()

	r := &roundTracker{}
	r.observe(2, true, false)
	require.Zero(t, r.readOnly)
	require.Equal(t, 1, r.totalRounds)

	r.observe(1, false, false)
	require.Equal(t, 1, r.readOnly)
	require.Equal(t, 1, r.singleCall)
	require.Equal(t, 1, r.noProgress)

	r.observe(1, false, true)
	require.Equal(t, 2, r.readOnly)
	require.Equal(t, 2, r.singleCall)
	require.Equal(t, 1, r.backgroundPolls)

	// A multi-call round breaks the serialisation streak.
	r.observe(3, false, false)
	require.Zero(t, r.singleCall)
	require.Zero(t, r.backgroundPolls)
	require.Equal(t, 3, r.readOnly)
}

func TestNewFilesTouched(t *testing.T) {
	t.Parallel()

	reads := fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolCallID: "1", ToolName: "view", Input: `{"file_path":"a.go"}`},
		fantasy.ToolCallContent{ToolCallID: "2", ToolName: "grep", Input: `{"query":"x"}`},
	}
	require.False(t, newFilesTouched(reads))

	edit := fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolCallID: "1", ToolName: "edit", Input: `{}`},
	}
	require.True(t, newFilesTouched(edit))

	// A shell write is a mutation even though the tool is "bash".
	write := fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolCallID: "1", ToolName: "bash", Input: `{"command":"sed -i 's/a/b/' f.go"}`},
	}
	require.True(t, newFilesTouched(write))

	build := fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolCallID: "1", ToolName: "bash", Input: `{"command":"go build ./..."}`},
	}
	require.False(t, newFilesTouched(build))
}

func TestShellArgumentsWrite(t *testing.T) {
	t.Parallel()

	writers := []string{
		"sed -i 's/a/b/' f.go",
		"perl -pi -e 's/a/b/' f.go",
		"echo x | tee out.txt",
		"echo x > out.txt",
		"echo x >> out.txt",
		"rm -rf build",
		"mv a b",
		"cp a b",
		"mkdir -p x",
		"touch x",
		"git add internal",
		"git commit -m 'x'",
		"gofmt -w .",
		"npm install",
		"go mod tidy",
		"chmod +x run.sh",
	}
	for _, command := range writers {
		require.True(t, shellArgumentsWrite(command), "should be a write: %q", command)
	}

	readers := []string{
		"go build ./...",
		"go test ./internal/agent/",
		"git status",
		"git diff",
		"ls -la",
		"grep -rn foo .",
	}
	for _, command := range readers {
		require.False(t, shellArgumentsWrite(command), "should not be a write: %q", command)
	}
}

func TestOnlyJobChecks(t *testing.T) {
	t.Parallel()

	require.False(t, onlyJobChecks(fantasy.ResponseContent{}))
	require.True(t, onlyJobChecks(fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolName: "job_output"},
	}))
	require.True(t, onlyJobChecks(fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolName: "job_output"},
		fantasy.ToolCallContent{ToolName: "agent_status"},
	}))
	require.False(t, onlyJobChecks(fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolName: "job_output"},
		fantasy.ToolCallContent{ToolName: "edit"},
	}))
}

func TestPostDecisionTail(t *testing.T) {
	t.Parallel()

	require.Zero(t, postDecisionTail(fantasy.ResponseContent{}))

	reasoning := fantasy.ResponseContent{
		fantasy.ReasoningContent{Text: "first thought\n\nsecond thought"},
	}
	tail := postDecisionTail(reasoning)
	require.Positive(t, tail)
	require.Less(t, tail, len("first thought\n\nsecond thought")+1)
}

func TestReasoningRestarts(t *testing.T) {
	t.Parallel()

	content := fantasy.ResponseContent{
		fantasy.ReasoningContent{Text: "Wait, that is wrong. Actually, let me reconsider. Hmm, no."},
	}
	// "wait," "actually," "let me reconsider" and "hmm," are all revisions.
	require.Equal(t, 4, reasoningRestarts(content))
	require.Zero(t, reasoningRestarts(fantasy.ResponseContent{}))
}

func TestTodoCounts(t *testing.T) {
	t.Parallel()

	open, total := todoCounts(nil)
	require.Zero(t, open)
	require.Zero(t, total)

	open, total = todoCounts([]todoState{
		{Content: "a", Status: "completed"},
		{Content: "b", Status: "cancelled"},
		{Content: "c", Status: "in_progress"},
		{Content: "d", Status: "pending"},
	})
	require.Equal(t, 2, open)
	require.Equal(t, 4, total)
}

func TestRepeatedProseCount(t *testing.T) {
	t.Parallel()

	require.Zero(t, repeatedProseCount("Nothing repeats here. Each sentence is new."))
	repeated := "The build fails on the missing import. The build fails on the missing import. Fix it."
	require.Equal(t, 1, repeatedProseCount(repeated))
}

func TestTurnGovernorReasoningScore(t *testing.T) {
	t.Parallel()

	state := governanceForTest(t, &config.GovernanceOptions{})
	turn := newTurnGovernor(state)

	turn.beginRound()
	turn.observeReasoning("thinking hard about the failure")
	turn.observeProse("the fix is in main.go")
	turn.recordReasoning(120, true, 1)

	score := turn.scoreSnapshot()
	require.Equal(t, 1, score.Rounds)
	require.Equal(t, 1, score.ThinkingRounds)
	require.Equal(t, 1, score.ProgressRounds)
	require.Positive(t, score.ReasonChars)
	require.Equal(t, 1, score.Restarts)
}

func TestTurnGovernorReasoningMetricsDisabled(t *testing.T) {
	t.Parallel()

	off := false
	state := governanceForTest(t, &config.GovernanceOptions{ReasoningMetrics: &off})
	turn := newTurnGovernor(state)
	turn.beginRound()
	turn.recordReasoning(100, true, 2)
	require.Zero(t, turn.scoreSnapshot().Rounds, "metrics are not kept when disabled")
}

func TestTurnGovernorNilMethodsAreSafe(t *testing.T) {
	t.Parallel()

	var turn *turnGovernor
	turn.beginRound()
	turn.observeReasoning("x")
	turn.observeProse("x")
	require.False(t, turn.observeToolCall("edit", "{}"))
	turn.observeToolResult("edit", false, "ok")
	require.Empty(t, turn.endRound(roundOutcome{}))
	require.Empty(t, turn.takeNotes())
	turn.recordReasoning(1, true, 1)
	require.Equal(t, 0, turn.scoreSnapshot().Rounds)
}
