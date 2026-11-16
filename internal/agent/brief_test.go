package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestContinuationPromptWithoutHistory(t *testing.T) {
	t.Parallel()

	prompt := buildContinuationPrompt(nil, "")
	require.Contains(t, prompt, "continuation brief")
	require.NotContains(t, prompt, "<previous_brief>")
	require.NotContains(t, prompt, "<current_plan>")
}

func TestContinuationPromptCarriesThePreviousBrief(t *testing.T) {
	t.Parallel()

	prompt := buildContinuationPrompt(nil, "## Established facts\n- the build needs BUILD_DIR")
	require.Contains(t, prompt, "<previous_brief>")
	require.Contains(t, prompt, "BUILD_DIR")
	require.Contains(t, prompt, "Keep every fact and constraint in it that is still true")
	require.Contains(t, prompt, "Do not repeat what it already says")
}

func TestContinuationPromptCarriesThePlan(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{
		{Content: "read the code", Status: session.TodoStatusCompleted},
		{Content: "change the loop", Status: session.TodoStatusPending},
	}
	prompt := buildContinuationPrompt(todos, "")
	require.Contains(t, prompt, "<current_plan>")
	require.Contains(t, prompt, "- [completed] read the code")
	require.Contains(t, prompt, "- [pending] change the loop")
	require.Contains(t, prompt, "continues the plan instead of restating it")
}

func TestSummaryPromptIsTheBrief(t *testing.T) {
	t.Parallel()

	// buildSummaryPrompt is what the summarize path calls; it must ask for the
	// brief, not for a generic summary.
	prompt := buildSummaryPrompt(nil)
	require.Contains(t, prompt, "continuation brief")
}

func TestBriefMemoIsReusedAcrossIdenticalPrefixes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{},
		DigestDir: dir,
		Provider:  "p",
		Model:     "m",
	})
	history := []fantasy.Message{fantasy.NewUserMessage("first"), fantasy.NewUserMessage("second")}

	chain := chainFor("", history)
	_, ok := state.summaryFor(chain)
	require.False(t, ok, "nothing has been written yet")

	state.rememberSummary(chain, len(history), "## Established facts\n- the cache is disabled in tests")
	brief, ok := state.summaryFor(chain)
	require.True(t, ok)
	require.Contains(t, brief, "cache is disabled")

	// A different prefix is a different brief.
	other := chainFor("", append(history, fantasy.NewUserMessage("third")))
	_, ok = state.summaryFor(other)
	require.False(t, ok)
}

func TestBriefMemoSurvivesARestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settings := &config.ContextReductionOptions{}
	first := newReductionState(&ReductionOptions{Settings: settings, DigestDir: dir, Provider: "p", Model: "m"})
	history := []fantasy.Message{fantasy.NewUserMessage("hello")}
	chain := chainFor("", history)
	first.rememberSummary(chain, 1, "## Goal\nship it")

	// A new process, the same digest directory.
	second := newReductionState(&ReductionOptions{Settings: settings, DigestDir: dir, Provider: "p", Model: "m"})
	brief, ok := second.summaryFor(chain)
	require.True(t, ok)
	require.Contains(t, brief, "ship it")
	_ = filepath.Join(dir, "digests")
}

func TestChainForCoversEveryPart(t *testing.T) {
	t.Parallel()

	base := []fantasy.Message{fantasy.NewUserMessage("hi")}
	withReasoning := []fantasy.Message{{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "hi"},
			fantasy.ReasoningPart{Text: "thinking"},
		},
	}}
	require.NotEqual(t, chainFor("", base), chainFor("", withReasoning))

	// Tool results are part of the absorbed prefix: a brief that ignored them
	// would be reused for a transcript it never saw.
	withResult := []fantasy.Message{{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{ToolCallID: "c1", Output: fantasy.ToolResultOutputContentText{Text: "output"}},
		},
	}}
	require.NotEqual(t, chainFor("", base), chainFor("", withResult))
}

func TestBriefPromptMentionsPointersNotBodies(t *testing.T) {
	t.Parallel()

	text := string(summaryPrompt)
	require.Contains(t, text, "Pointers, not bodies")
	require.Contains(t, text, "Already ruled out")
	require.Contains(t, text, "Session rules")
	require.Contains(t, text, "Never mention that this is a summary")
	require.NotContains(t, strings.ToLower(text), "be thorough",
		"the old prompt asked for length, which is the cost this brief exists to avoid")
}

func TestAbsorbedPrefixStopsAtTheBrief(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{ID: "old-1"},
		{ID: "brief"},
		{ID: "after-1"},
		{ID: "after-2"},
	}
	require.Equal(t, 2, absorbedPrefix(msgs, "brief"),
		"the brief itself is absorbed, so the delta starts after it")
	require.Equal(t, 0, absorbedPrefix(msgs, ""),
		"a session that was never compacted has nothing absorbed")
	require.Equal(t, 0, absorbedPrefix(msgs, "missing"),
		"an unknown summary id must not silently skip the transcript")
}

// TestDeltaFoldReadsOnlyWhatTheBriefMissed covers the cost of a repeated
// compaction: the previous brief already states what the folded prefix
// established, so only the messages after it may be sent to the summarizer.
func TestDeltaFoldReadsOnlyWhatTheBriefMissed(t *testing.T) {
	t.Parallel()

	model := &recordingStreamModel{text: "## Established facts\n- the delta was folded"}
	env := testEnv(t)
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system").(*sessionAgent)
	sa.reduction = newReductionState(&ReductionOptions{
		Settings:  &config.ContextReductionOptions{},
		DigestDir: t.TempDir(),
		Provider:  "fake",
		Model:     "fake-model",
	})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)

	// A compaction that already happened: one folded turn plus its brief.
	_, err = sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "folded earlier"})
	require.NoError(t, err)
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))

	first := model.lastCall()
	require.NotEmpty(t, first)
	require.Contains(t, promptText(first), "folded earlier",
		"the first compaction reads the whole transcript")

	brief, err := env.messages.Get(ctx, mustSession(t, env, sess.ID).SummaryMessageID)
	require.NoError(t, err)
	model.reset()

	// New turns after the compaction.
	_, err = sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "work after the brief"})
	require.NoError(t, err)

	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))
	second := model.lastCall()
	require.NotEmpty(t, second)

	prompt := promptText(second)
	require.Contains(t, prompt, "work after the brief",
		"the delta after the brief is what still needs summarizing")
	require.NotContains(t, prompt, "folded earlier",
		"the folded turn is the brief's job now, not the summarizer's")
	require.Contains(t, prompt, strings.TrimSpace(brief.Content().Text),
		"the previous brief is carried into the prompt so its facts survive")
}

// TestSecondCompactionIsFreeWhenNothingChanged covers the case that made
// compaction expensive: running it again over a transcript that has not moved
// must not send a prompt at all.
func TestSecondCompactionIsFreeWhenNothingChanged(t *testing.T) {
	t.Parallel()

	model := &recordingStreamModel{text: "## Goal\n- stay put"}
	env := testEnv(t)
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system").(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "session")
	require.NoError(t, err)
	_, err = sa.Run(ctx, SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))

	model.reset()
	require.NoError(t, sa.Summarize(ctx, sess.ID, nil, nil))
	require.Empty(t, model.calls(), "an unchanged transcript has nothing left to summarize")
}

// recordingStreamModel is a minimal LanguageModel that records the messages it
// was asked to read, so a test can assert what the summarizer actually saw.
type recordingStreamModel struct {
	finishStreamModel
	mu      sync.Mutex
	prompts []fantasy.Prompt
}

func (m *recordingStreamModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.prompts = append(m.prompts, call.Prompt)
	m.mu.Unlock()
	return m.finishStreamModel.Stream(ctx, call)
}

func (m *recordingStreamModel) reset() {
	m.mu.Lock()
	m.prompts = nil
	m.mu.Unlock()
}

func (m *recordingStreamModel) calls() []fantasy.Prompt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]fantasy.Prompt(nil), m.prompts...)
}

func (m *recordingStreamModel) lastCall() fantasy.Prompt {
	calls := m.calls()
	if len(calls) == 0 {
		return nil
	}
	return calls[len(calls)-1]
}

// promptText flattens every text part of a recorded prompt.
func promptText(prompt fantasy.Prompt) string {
	var sb strings.Builder
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				sb.WriteString(text.Text)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

func mustSession(t *testing.T, env fakeEnv, sessionID string) session.Session {
	t.Helper()
	sess, err := env.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	return sess
}

func TestSummaryMemoNilStateIsSafe(t *testing.T) {
	t.Parallel()

	var state *reductionState
	_, ok := state.summaryFor("chain")
	require.False(t, ok)
	state.rememberSummary("chain", 1, "brief")

	agent := &sessionAgent{}
	require.Nil(t, agent.reduction)
	_ = context.Background()
}
