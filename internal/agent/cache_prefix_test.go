package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// cachePrefix renders the messages a provider sees, as text, so a test can
// compare byte-for-byte prefixes. A hit requires the prefix to match from the
// first token, so this is the property that decides caching, not a metaphore.
func cachePrefix(t *testing.T, sa *sessionAgent, env fakeEnv, sessID string) string {
	t.Helper()
	s, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	msgs, err := sa.getSessionMessages(t.Context(), s)
	require.NoError(t, err)
	ai, _ := sa.preparePrompt(msgs, false, false)
	ai, _ = sa.reducePrompt("SYS", ai, 200000)
	var out strings.Builder
	for _, m := range ai {
		out.WriteString(string(m.Role))
		for _, p := range m.Content {
			fmt.Fprintf(&out, "|%v", p)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// TestPromptPrefixSurvivesNewTurns is the cache contract: every byte the
// provider already saw must stay identical when a turn is added. Digesting an
// older tool result for its own sake used to rewrite the prefix and lose the
// cache, which costs more than the stale body saved.
func TestPromptPrefixSurvivesNewTurns(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, &recordingStreamModel{text: "reply"}, &finishStreamModel{text: "t"}, "SYS").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings: &config.ContextReductionOptions{
			CompactionTriggerTokens: intPtr(180000),
			CompactionBudgetTokens:  intPtr(90000),
			ToolResultDigestTokens:  intPtr(100),
		},
		DigestDir: t.TempDir(),
		Provider:  "p",
		Model:     "m",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)

	addTurn := func(n int) {
		_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: fmt.Sprintf("turn %d", n)}},
		})
		require.NoError(t, err)
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "checking"},
				message.ToolCall{ID: fmt.Sprintf("c%d", n), Name: "bash", Input: `{"command":"go test ./..."}`, Finished: true},
			},
		})
		require.NoError(t, err)
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID: fmt.Sprintf("c%d", n),
				Content:    strings.Repeat("test output line\n", 60),
			}},
		})
		require.NoError(t, err)
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "done"}},
		})
		require.NoError(t, err)
	}

	addTurn(1)
	first := cachePrefix(t, sa, env, sess.ID)
	addTurn(2)
	second := cachePrefix(t, sa, env, sess.ID)

	require.True(t, strings.HasPrefix(second, first),
		"a new turn must not change any byte of the previous prompt")
}
