package contextreduction

import (
	"charm.land/fantasy"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hugeTail builds a transcript whose final messages are large tool results,
// the shape that made a 90K compaction arrive at 160K+.
func hugeTail() []fantasy.Message {
	return []fantasy.Message{
		fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.ToolCallPart{ToolCallID: "c1", ToolName: "bash", Input: `{}`},
		}},
		fantasy.Message{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "c1", Output: fantasy.ToolResultOutputContentText{Text: strings.Repeat("a very long test output line\n", 12000)}},
		}},
		fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "done"}}},
		fantasy.Message{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "c2", Output: fantasy.ToolResultOutputContentText{Text: strings.Repeat("another long output line\n", 12000)}},
		}},
	}
}

// TestFitTailToBudgetBoundsRecentResults checks the protected recent tail is
// bounded instead of being kept verbatim at any size.
func TestFitTailToBudgetBoundsRecentResults(t *testing.T) {
	t.Parallel()

	msgs := hugeTail()
	before := Summarize("", msgs).Total()
	require.Greater(t, before, 150_000)

	fitted, changed := FitTailToBudget(msgs, 90_000)
	require.True(t, changed)
	after := Summarize("", fitted).Total()
	require.LessOrEqual(t, after, 90_000,
		"a 90K budget must not produce a 160K prompt")
	require.Less(t, after, before)
}

// TestFitTailToBudgetKeepsTextVerbatim checks the reduction is limited to tool
// results: assistant text in the protected tail must not be changed.
func TestFitTailToBudgetKeepsTextVerbatim(t *testing.T) {
	t.Parallel()

	msgs := hugeTail()
	_, changed := FitTailToBudget(msgs, 90_000)
	require.True(t, changed)
	require.Equal(t, "done", msgs[2].Content[0].(fantasy.TextPart).Text)
}

// TestCompactionWithHugeRecentResultsStaysNearBudget is the end-to-end guard
// for the reported overshoot: a compaction targeting 90K used to arrive at
// 160K because the two messages it had to keep were enormous tool results.
func TestCompactionWithHugeRecentResultsStaysNearBudget(t *testing.T) {
	t.Parallel()

	msgs := []fantasy.Message{
		userText("start"),
		toolCall("c1", "bash", `{}`),
		toolResult("c1", strings.Repeat("a very long test output line\n", 12000)),
		userText("continue"),
		toolCall("c2", "bash", `{}`),
		toolResult("c2", strings.Repeat("another long output line\n", 12000)),
	}
	before := Summarize("", msgs).Total()
	require.Greater(t, before, 150_000)

	opts := CompactOptions{
		// A trigger below the transcript so compaction actually runs.
		TriggerTokens:  150_000,
		BudgetTokens:   90_000,
		OverheadTokens: 7_000,
	}
	_, plan := CompactLocally("", msgs, "", opts)
	require.True(t, plan.Compact)

	// The planner keeps the protected tail; fitting it is what brings the
	// built prompt close to the budget.
	tail := msgs[len(msgs)-max(2, plan.KeptMessages):]
	fitted, changed := FitTailToBudget(tail, opts.BudgetTokens-opts.OverheadTokens)
	require.True(t, changed)
	require.LessOrEqual(t, Summarize("", fitted).Total(), opts.BudgetTokens,
		"the built prompt must land near the configured budget")
}

// TestHugeRecentResultsAreBoundedByBudget checks a 1M-token window cannot let
// a 90K budget send 100K+ tokens. The window ceiling used to win over the
// budget, so enormous tool results in the recent tail passed through.
func TestHugeRecentResultsAreBoundedByBudget(t *testing.T) {
	t.Parallel()

	msgs := hugeTail()
	before := Summarize("", msgs).Total()
	require.Greater(t, before, 150_000)

	// This is the bound repack now applies: the smaller of the model window
	// ceiling and the user's compaction budget.
	ceiling := 900_000 * 9 / 10
	budget := 90_000
	limit := min(ceiling, budget)

	fitted, changed := FitTailToBudget(msgs, limit)
	require.True(t, changed)
	require.LessOrEqual(t, Summarize("", fitted).Total(), budget,
		"one edit's results must not cost 100K tokens under a 90K budget")
}
