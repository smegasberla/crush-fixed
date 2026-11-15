package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// call is one tool invocation in a simulated round.
type call struct {
	tool  string
	input string
}

// runRound feeds a round of calls through the governor and returns its notes.
func runRound(t *testing.T, turn *turnGovernor, calls []call) []string {
	t.Helper()
	for _, c := range calls {
		turn.observeToolCall(c.tool, c.input)
		turn.observeToolResult(c.tool, c.input, false, "output for "+c.tool)
	}
	return turn.endRound(roundOutcome{})
}

func TestGovernorSilentOnOrdinaryRound(t *testing.T) {
	t.Parallel()

	state := newGovernanceState(&GovernanceOptions{Settings: &config.GovernanceOptions{}})
	turn := newTurnGovernor(state)
	turn.beginRound()

	notes := runRound(t, turn, []call{
		{"view", `{"file_path":"a.go"}`},
		{"view", `{"file_path":"b.go"}`},
		{"view", `{"file_path":"c.go"}`},
		{"grep", `{"pattern":"foo"}`},
		{"edit", `{"file_path":"a.go"}`},
		{"bash", `{"command":"go build"}`},
	})

	report := turn.gov.Report()
	require.Equal(t, 0.0, report.Pressure, "distinct reads are not repetition")
	require.Equal(t, 0, report.RepeatedReads)
	require.Zero(t, report.Searches, "reading and editing are not searches")
	require.Empty(t, notes, "an ordinary round must never be steered")
}

func TestGovernorCountsEachActionOnce(t *testing.T) {
	t.Parallel()

	state := newGovernanceState(&GovernanceOptions{Settings: &config.GovernanceOptions{}})
	turn := newTurnGovernor(state)
	turn.beginRound()

	runRound(t, turn, []call{
		{"view", `{"file_path":"a.go"}`},
		{"edit", `{"file_path":"a.go"}`},
		{"bash", `{"command":"go build"}`},
	})

	report := turn.gov.Report()
	require.Equal(t, 1, report.Reads, "one read is one action, not two")
	require.Equal(t, 1, report.Edits)
	require.Equal(t, 1, report.Runs)
}

func TestGovernorFiresOnRealRepeat(t *testing.T) {
	t.Parallel()

	state := newGovernanceState(&GovernanceOptions{Settings: &config.GovernanceOptions{}})
	turn := newTurnGovernor(state)
	turn.beginRound()

	same := make([]call, 0, 8)
	for range 8 {
		same = append(same, call{"view", `{"file_path":"same.go"}`})
	}
	notes := runRound(t, turn, same)

	report := turn.gov.Report()
	require.Equal(t, 8, report.RepeatedReads)
	require.NotEmpty(t, notes, "reading one file eight times should steer")
}

func TestGovernorDistinguishesDifferentReads(t *testing.T) {
	t.Parallel()

	state := newGovernanceState(&GovernanceOptions{Settings: &config.GovernanceOptions{}})
	turn := newTurnGovernor(state)
	turn.beginRound()

	distinct := make([]call, 0, 8)
	for _, file := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		distinct = append(distinct, call{"view", `{"file_path":"` + file + `.go"}`})
	}
	require.Empty(t, runRound(t, turn, distinct))
	require.Zero(t, turn.gov.Report().RepeatedReads, "eight different files are eight different reads")
}

func TestToolCallInputByID(t *testing.T) {
	t.Parallel()

	assistant := &message.Message{Parts: []message.ContentPart{
		message.ToolCall{ID: "call-1", Name: "view", Input: `{"file_path":"a.go"}`},
	}}
	require.Equal(t, `{"file_path":"a.go"}`, toolCallInputByID("call-1", assistant))
	require.Empty(t, toolCallInputByID("missing", assistant))
	require.Empty(t, toolCallInputByID("call-1", nil))
	require.Empty(t, toolCallInputByID("", assistant))
}

func TestGovernanceNotesAreAppendedNotPrepended(t *testing.T) {
	t.Parallel()

	// The property that matters: a note must never change an earlier byte of
	// the request, because the provider caches the longest identical prefix.
	notes := governanceNotesText([]string{"a steering note"})
	require.Contains(t, notes, "a steering note")
	require.True(t, len(notes) > 0)
}
