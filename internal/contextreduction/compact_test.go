package contextreduction

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// messagesOfSize builds n user messages of roughly tokens each.
func messagesOfSize(n, tokens int) []fantasy.Message {
	text := strings.Repeat("x", tokens*BytesPerToken)
	out := make([]fantasy.Message, 0, n)
	for range n {
		out = append(out, userText(text))
	}
	return out
}

func TestCompactNoOpUnderTrigger(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(3, 100)
	out, plan := Compact("", messages, 50, CompactOptions{TriggerTokens: 10000})
	require.False(t, plan.Compact)
	require.Equal(t, 0, plan.TailStart)
	require.Equal(t, len(messages), len(out))
	require.Equal(t, "within trigger", plan.Reason)
	require.Equal(t, 300, plan.Total)
}

func TestCompactDropsTheOldestMessages(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(40, 1000)
	opts := CompactOptions{TriggerTokens: 5000, BudgetTokens: 8000, ReserveTokens: 1000}
	out, plan := Compact("", messages, 500, opts)

	require.True(t, plan.Compact)
	require.Positive(t, plan.DroppedMessages)
	require.Equal(t, len(messages)-plan.DroppedMessages, len(out))
	// 8000 - 1000 reserve - 500 summary = 6500 tokens of tail.
	require.LessOrEqual(t, Summarize("", out).Total(), 6500)
	// The newest message always survives.
	require.Equal(t, messages[len(messages)-1], out[len(out)-1])
}

func TestCompactKeepsMinimumTail(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(10, 10000)
	opts := CompactOptions{TriggerTokens: 1000, BudgetTokens: 1000, ReserveTokens: 500, MinTailMessages: 3}
	out, plan := Compact("", messages, 100, opts)
	require.True(t, plan.Compact)
	require.GreaterOrEqual(t, len(out), 3, "a summary with nothing to continue from is useless")
	require.Equal(t, messages[7], out[0])
}

func TestCompactHonoursMinTailMessages(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(5, 1000)
	opts := CompactOptions{TriggerTokens: 100, BudgetTokens: 100, ReserveTokens: 50, MinTailMessages: 4}
	_, plan := Compact("", messages, 10, opts)
	require.Equal(t, 4, plan.KeptMessages, "a tail smaller than the minimum is never produced")
	require.Equal(t, 1, plan.DroppedMessages)
}

func TestCompactMaxTailMessages(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(20, 10)
	opts := CompactOptions{TriggerTokens: 100, BudgetTokens: 10000, ReserveTokens: 1, MaxTailMessages: 5}
	out, plan := Compact("", messages, 1, opts)
	require.Len(t, out, 5)
	require.Equal(t, 15, plan.DroppedMessages)
}

func TestSummaryNote(t *testing.T) {
	t.Parallel()

	note := SummaryNote("## brief\nall good", 12, 3)
	require.Contains(t, note, "## brief")
	require.Contains(t, note, "12 earlier messages were compacted out")
	require.Contains(t, note, "3 messages kept verbatim")

	empty := SummaryNote("   ", 4, 0)
	require.Contains(t, empty, "4 earlier messages")
	require.NotContains(t, empty, "kept verbatim")
}

func TestFitToCeilingShrinksToolResults(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("tool output line\n", 500)
	messages := []fantasy.Message{
		toolCall("c1", "bash", `{}`),
		toolResult("c1", body),
		userText("next"),
		userText("next2"),
	}
	before := Summarize("", messages).Total()
	out, report := FitToCeiling("", messages, before/2, DigestOptions{MinTokens: 10}, PointerOptions{})
	require.True(t, report.Digest.Digested > 0)
	require.Less(t, report.TokensAfter, report.TokensBefore)
	require.False(t, report.OverBudget)
	require.Contains(t, resultTextOf(t, out[1]), "digested")
}

func TestFitToCeilingDropsOldMessagesLast(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(30, 100)
	report0 := Summarize("", messages).Total()
	out, report := FitToCeiling("", messages, report0/3, DigestOptions{}, PointerOptions{})
	require.Positive(t, report.DroppedMessages)
	require.Equal(t, DroppedAllNote, firstText(out))
	require.LessOrEqual(t, report.TokensAfter, report.Ceiling+1)
}

func TestFitToCeilingNoOpWhenWithin(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(2, 10)
	out, report := FitToCeiling("", messages, 100000, DigestOptions{}, PointerOptions{})
	require.Equal(t, messages, out)
	require.Equal(t, report.TokensBefore, report.TokensAfter)
	require.Zero(t, report.DroppedMessages)
}

func TestFitToCeilingZeroCeilingIsNoOp(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(2, 10)
	out, report := FitToCeiling("", messages, 0, DigestOptions{}, PointerOptions{})
	require.Equal(t, messages, out)
	require.Equal(t, report.TokensBefore, report.TokensAfter)
}

func TestFitToCeilingNeverDropsTheLastTwo(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(6, 100)
	out, report := FitToCeiling("", messages, 1, DigestOptions{}, PointerOptions{})
	require.Equal(t, 4, report.DroppedMessages)
	// One note plus the newest two messages.
	require.Len(t, out, 3)
	require.Equal(t, messages[5], out[2])
}

func firstText(messages []fantasy.Message) string {
	for _, msg := range messages {
		for _, part := range msg.Content {
			if text, ok := part.(fantasy.TextPart); ok {
				return text.Text
			}
		}
	}
	return ""
}

// TestCompactLocallyShrinksThePrompt is the property the whole design exists
// for: after a compaction the prompt must be materially smaller, and it must
// land at the configured budget rather than at the size that triggered it.
func TestCompactLocallyShrinksThePrompt(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(60, 1000)
	opts := CompactOptions{TriggerTokens: 45000, BudgetTokens: 20000, ReserveTokens: 8192}
	out, plan := CompactLocally("", messages, "", opts)

	require.True(t, plan.Compact, "a prompt over the trigger must compact")
	require.Greater(t, plan.Total, opts.TriggerTokens)
	require.NotEmpty(t, out)

	after := Summarize("", out).Total()
	require.Less(t, after, plan.Total, "compaction must shrink the prompt")
	require.LessOrEqual(t, after, opts.BudgetTokens+2000,
		"the kept tail must land near the budget, not near the trigger")
	require.Contains(t, out[0].Content[0].(fantasy.TextPart).Text, "compacted",
		"the window opens with the note that explains the fold")
}

// TestCompactLocallyKeepsTheAnswerHeadroomOutOfTheBudget pins the arithmetic:
// the reserve is headroom for the answer, so it must not be subtracted from the
// budget, which would fold further than the user asked for.
func TestCompactLocallyKeepsTheAnswerHeadroomOutOfTheBudget(t *testing.T) {
	t.Parallel()

	messages := messagesOfSize(60, 1000)
	base := CompactOptions{TriggerTokens: 45000, BudgetTokens: 20000, ReserveTokens: 0}
	withReserve := base
	withReserve.ReserveTokens = 8000

	_, planBase := CompactLocally("", messages, "", base)
	_, planReserve := CompactLocally("", messages, "", withReserve)

	require.Equal(t, planBase.KeptMessages, planReserve.KeptMessages,
		"the reserve is answer headroom and must not change what is kept")
}
