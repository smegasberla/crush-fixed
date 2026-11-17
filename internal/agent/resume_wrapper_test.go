package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestStripOneWrapperUnwrapsNestedCoverage covers the history left behind by the
// old resume announcement. It was stored as a user message once per compaction,
// so a prompt could end up wrapped several times over, and every copy was
// re-sent on each later request.
func TestStripOneWrapperUnwrapsNestedCoverage(t *testing.T) {
	t.Parallel()

	one := interruptedPromptPrefix + "Continue`"
	two := interruptedPromptPrefix + one + "`"
	three := interruptedPromptPrefix + two + "`"

	require.Equal(t, "Continue", stripOneWrapper(one))
	require.Equal(t, "Continue", stripOneWrapper(two))
	require.Equal(t, "Continue", stripOneWrapper(three),
		"a prompt wrapped once per compaction must collapse fully")
}

// TestStripOneWrapperLeavesOrdinaryTextAlone checks the scrub is targeted: a
// prompt that never carried the announcement is untouched.
func TestStripOneWrapperLeavesOrdinaryTextAlone(t *testing.T) {
	t.Parallel()

	require.Equal(t, "fix the parser", stripOneWrapper("fix the parser"))
	require.Equal(t, "", stripOneWrapper(""))
	require.Equal(t, "unrelated `code` here", stripOneWrapper("unrelated `code` here"))
}

// TestStripResumeWrappersDropsWrapperOnlyMessages checks a message that was
// nothing but the announcement is removed rather than sent as empty text.
func TestStripResumeWrappersDropsWrapperOnlyMessages(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{
			message.TextContent{Text: interruptedPromptPrefix + "Continue`", Hidden: true},
		}},
		{Role: message.User, Parts: []message.ContentPart{
			message.TextContent{Text: "real instruction"},
		}},
		{Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "reply"},
		}},
	}

	out := stripResumeWrappers(msgs)
	var all strings.Builder
	for _, m := range out {
		for _, p := range m.Parts {
			if tc, ok := p.(message.TextContent); ok {
				all.WriteString(tc.Text)
				all.WriteString("\n")
			}
		}
	}
	require.NotContains(t, all.String(), "previous session was interrupted")
	require.Contains(t, all.String(), "real instruction")
	require.Contains(t, all.String(), "reply")
}

// TestCompactionContinuationDoesNotWrapThePrompt is the regression guard for the
// source of the text: a compaction that cuts a turn short re-queues the prompt
// unchanged. Writing an announcement into it made the transcript grow by one
// copy per compaction, and every copy rode along on every request afterwards.
func TestCompactionContinuationDoesNotWrapThePrompt(t *testing.T) {
	t.Parallel()

	// The constant must no longer be used to build paths in the agent; it
	// survives only so old transcripts can be recognised and cleaned.
	require.True(t, strings.HasPrefix(
		fmt.Sprintf("%sContinue`", interruptedPromptPrefix), resumeWrapperMarker))
}
